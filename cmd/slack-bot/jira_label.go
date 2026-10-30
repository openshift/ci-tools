package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackutilsx"
)

const (
	jiraReadyLabel       = "dptp-ai-ready"
	jiraTokenHeader      = "X-Jira-Automation-Token"
	jiraSlackPostTimeout = 10 * time.Second
)

var jiraIssueKeyPattern = regexp.MustCompile(`^DPTP-[1-9][0-9]*$`)
var slackMemberIDPattern = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

type jiraLabelMessage struct {
	IssueKey string `json:"issueKey"`
	IssueURL string `json:"issueURL"`
	Summary  string `json:"summary"`
	Label    string `json:"label"`
}

type jiraLabelSlackPoster interface {
	PostMessageContext(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error)
}

type jiraLabelHandler struct {
	token         func() []byte
	channelID     string
	chaiBotUserID string
	poster        jiraLabelSlackPoster
}

func (h *jiraLabelHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	expectedToken := h.token()
	providedToken := []byte(request.Header.Get(jiraTokenHeader))
	if len(expectedToken) == 0 || subtle.ConstantTimeCompare(providedToken, expectedToken) != 1 {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}

	var message jiraLabelMessage
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		http.Error(writer, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		http.Error(writer, "expected one JSON object", http.StatusBadRequest)
		return
	}
	if !validJiraLabelMessage(message) {
		http.Error(writer, "invalid Jira label event", http.StatusBadRequest)
		return
	}

	text := formatJiraLabelMessage(message, h.chaiBotUserID)
	ctx, cancel := context.WithTimeout(request.Context(), jiraSlackPostTimeout)
	defer cancel()
	if _, _, err := h.poster.PostMessageContext(ctx, h.channelID, slack.MsgOptionText(text, false), slack.MsgOptionDisableLinkUnfurl()); err != nil {
		logrus.WithError(err).WithField("issue_key", message.IssueKey).Error("failed to post Jira label notification")
		http.Error(writer, "failed to post Slack message", http.StatusBadGateway)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func formatJiraLabelMessage(message jiraLabelMessage, chaiBotUserID string) string {
	return fmt.Sprintf("<@%s> Please implement the work described in this Jira issue and open a pull request with the changes: <%s|%s> — %s", chaiBotUserID, message.IssueURL, message.IssueKey, slackutilsx.EscapeMessage(message.Summary))
}

func validJiraLabelMessage(message jiraLabelMessage) bool {
	if message.Label != jiraReadyLabel || !jiraIssueKeyPattern.MatchString(message.IssueKey) || message.Summary == "" {
		return false
	}
	issueURL, err := url.Parse(message.IssueURL)
	if err != nil {
		return false
	}
	return issueURL.Scheme == "https" && issueURL.Host == "redhat.atlassian.net" && issueURL.EscapedPath() == "/browse/"+message.IssueKey && issueURL.RawQuery == "" && issueURL.Fragment == "" && issueURL.User == nil
}
