package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/slack-go/slack"
)

const (
	GitHubOrganization = "masmovil"
)

type Commit struct {
	url            string
	authorUsername string
	authorEmail    string
	commitMessage  string
}

func (c Commit) getCommitMessageTitle() string {
	return strings.Split(c.commitMessage, "\n")[0]
}

type CommitStatus struct {
	Name        string
	Description string
	Conclusion  string
	Url         string
}

func (o CommitStatus) Succeeded() bool {
	return o.Conclusion == "success"
}

func (o CommitStatus) Failed() bool {
	return o.Conclusion == "failure"
}

// GithubUserSSO is used to unmarshall GitHub API response
type GithubUserSSO struct {
	Data struct {
		Organization struct {
			SAMLIdentityProvider struct {
				ExternalIdentities struct {
					Edges []struct {
						Node struct {
							SamlIdentity struct {
								NameId string `json:"nameId"`
							} `json:"samlIdentity"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"externalIdentities"`
			} `json:"samlIdentityProvider"`
		} `json:"organization"`
	} `json:"data"`
}

func main() {
	fmt.Println("Running actions-notify-slack")

	slackClient := getSlackClient()
	commit := buildCommit()
	commitStatus := buildCommitStatus()
	message := buildMessage(slackClient, commit, commitStatus)

	sendMessage(slackClient, message)
	return
}

func getSlackClient() (client *slack.Client) {
	accessToken := os.Getenv("SLACK_ACCESS_TOKEN")
	client = slack.New(accessToken)
	return client
}

func buildMessage(client *slack.Client, commit Commit, commitStatus CommitStatus) (message string) {
	slackUser, err := client.GetUserByEmail(commit.authorEmail)
	if err != nil {
		fmt.Println("got error getting slack user by email, defaulting to nil:", err)
		slackUser = nil
	}
	userMention := buildUserMention(slackUser, commit.authorUsername)

	message = fmt.Sprintf(":warning: The commit <%s|\"_%s_\"> by %s has failed the pipeline step <%s|%s>",
		commit.url,
		commit.getCommitMessageTitle(),
		userMention,
		commitStatus.Url,
		commitStatus.Name,
	)
	return
}

func buildUserMention(slackUser *slack.User, githubAuthorUsername string) (mention string) {
	githubAuthorUrl := "https://github.com/" + githubAuthorUsername
	if slackUser != nil {
		mention += fmt.Sprintf("<@%s> ([%s](%s))", slackUser.ID, githubAuthorUsername, githubAuthorUrl)
	} else {
		mention += fmt.Sprintf("[%s](%s)", githubAuthorUsername, githubAuthorUrl)
	}
	return mention
}

func buildCommitStatus() (commitStatus CommitStatus) {
	commitStatus = CommitStatus{
		Name:        os.Getenv("STATUS_NAME"),
		Description: os.Getenv("STATUS_DESCRIPTION"),
		Conclusion:  os.Getenv("STATUS_CONCLUSION"),
		Url:         os.Getenv("STATUS_URL"),
	}
	return
}

func buildCommit() (commit Commit) {
	commit = Commit{
		url:            os.Getenv("COMMIT_URL"),
		authorUsername: os.Getenv("COMMIT_AUTHOR_USERNAME"),
		authorEmail:    os.Getenv("COMMIT_AUTHOR_EMAIL"),
		commitMessage:  os.Getenv("COMMIT_MESSAGE"),
	}

	authorEmail, err := getAuthorEmailFromGithubSSO(commit.authorUsername)
	if err != nil {
		// If we are unable to get email from GitHub SSO, we will use the one specified in the commit metadata
		fmt.Println("got error getting email from github SSO:", err)
		return
	}
	// Replace the email from the commit with the one from GitHub SSO
	commit.authorEmail = authorEmail

	return
}

func getAuthorEmailFromGithubSSO(authorUsername string) (authorEmail string, err error) {
	// Get email from organization SSO, using GitHub username as key
	queryBody := fmt.Sprintf("{\"query\": \"query {organization(login: \\\"%s\\\"){samlIdentityProvider{externalIdentities(first: 1, login: \\\"%s\\\") {edges {node {samlIdentity {nameId}}}}}}}\"}", GitHubOrganization, authorUsername)
	req, err := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewBuffer([]byte(queryBody)))
	accessToken := os.Getenv("GITHUB_ACCESS_TOKEN")
	req.Header.Add("Authorization", "Bearer "+accessToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("got error while doing request to github API:", err)
		return
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			fmt.Println("got error closing github API response body:", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("got error reading github API response body:", err)
		return
	}

	var githubAuthorSSO GithubUserSSO
	err = json.Unmarshal(body, &githubAuthorSSO)
	if err != nil {
		fmt.Println("got error unmarshalling github API response body:", err)
		return
	}

	if len(githubAuthorSSO.Data.Organization.SAMLIdentityProvider.ExternalIdentities.Edges) == 0 {
		err = errors.New("no external identity edges")
		fmt.Println("got zero external identity edges from github api response:", err)
		return
	}

	authorEmail = githubAuthorSSO.Data.Organization.SAMLIdentityProvider.ExternalIdentities.Edges[0].Node.SamlIdentity.NameId
	return
}

func sendMessage(client *slack.Client, message string) {
	slackChannel := os.Getenv("SLACK_CHANNEL_NAME")

	respChannel, respTimestamp, err := client.PostMessage(slackChannel, slack.MsgOptionText(message, false), slack.MsgOptionAsUser(true))
	if err != nil {
		fmt.Println("got error posting message to slack:", err)
		return
	}
	fmt.Println("Message sent to channel", respChannel, "at", respTimestamp)
	return
}
