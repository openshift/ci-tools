package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

type recordingJiraLabelPoster struct {
	channelID string
	posts     int
	err       error
	deadline  time.Time
}

func (poster *recordingJiraLabelPoster) PostMessageContext(ctx context.Context, channelID string, _ ...slack.MsgOption) (string, string, error) {
	poster.channelID = channelID
	poster.posts++
	poster.deadline, _ = ctx.Deadline()
	return "", "", poster.err
}

func jiraLabelToken() []byte {
	return []byte("test-token")
}

func TestJiraLabelEndpoint(t *testing.T) {
	const validBody = `{"issueKey":"DPTP-123","issueURL":"https://redhat.atlassian.net/browse/DPTP-123","summary":"Ready for AI","label":"dptp-ai-ready"}`
	testCases := []struct {
		name       string
		method     string
		body       string
		token      string
		posterErr  error
		wantStatus int
		wantPosts  int
	}{
		{
			name:       "valid notification",
			method:     http.MethodPost,
			body:       validBody,
			token:      "test-token",
			wantStatus: http.StatusNoContent,
			wantPosts:  1,
		},
		{
			name:       "missing token",
			method:     http.MethodPost,
			body:       validBody,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong token",
			method:     http.MethodPost,
			body:       validBody,
			token:      "wrong-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong method",
			method:     http.MethodGet,
			token:      "test-token",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "wrong label",
			method:     http.MethodPost,
			body:       strings.Replace(validBody, "dptp-ai-ready", "other-label", 1),
			token:      "test-token",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "other Jira project",
			method:     http.MethodPost,
			body:       strings.ReplaceAll(validBody, "DPTP-123", "OTHER-123"),
			token:      "test-token",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "untrusted issue URL",
			method:     http.MethodPost,
			body:       strings.Replace(validBody, "redhat.atlassian.net", "evil.example.com", 1),
			token:      "test-token",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid JSON",
			method:     http.MethodPost,
			body:       `{`,
			token:      "test-token",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Slack failure",
			method:     http.MethodPost,
			body:       validBody,
			token:      "test-token",
			posterErr:  errors.New("Slack unavailable"),
			wantStatus: http.StatusBadGateway,
			wantPosts:  1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			poster := &recordingJiraLabelPoster{err: testCase.posterErr}
			handler := &jiraLabelHandler{
				token:         jiraLabelToken,
				channelID:     "C-team-dp-testplatform",
				chaiBotUserID: "U0AKNPBBVT7",
				poster:        poster,
			}
			request := httptest.NewRequest(testCase.method, "/jira/label-endpoint", strings.NewReader(testCase.body))
			if testCase.token != "" {
				request.Header.Set(jiraTokenHeader, testCase.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, testCase.wantStatus)
			}
			if poster.posts != testCase.wantPosts {
				t.Fatalf("posts = %d, want %d", poster.posts, testCase.wantPosts)
			}
			if poster.posts > 0 && poster.channelID != "C-team-dp-testplatform" {
				t.Fatalf("channel = %q, want configured channel", poster.channelID)
			}
			if poster.posts > 0 && (poster.deadline.IsZero() || time.Until(poster.deadline) > jiraSlackPostTimeout) {
				t.Fatalf("Slack post deadline = %s, want a deadline within %s", poster.deadline, jiraSlackPostTimeout)
			}
		})
	}
}

func TestFormatJiraLabelMessageEscapesSummary(t *testing.T) {
	message := jiraLabelMessage{
		IssueKey: "DPTP-123",
		IssueURL: "https://redhat.atlassian.net/browse/DPTP-123",
		Summary:  "A <@U123> & B",
		Label:    jiraReadyLabel,
	}
	want := "<@U0AKNPBBVT7> Please implement the work described in this Jira issue and open a pull request with the changes: <https://redhat.atlassian.net/browse/DPTP-123|DPTP-123> — A &lt;@U123&gt; &amp; B"
	if got := formatJiraLabelMessage(message, "U0AKNPBBVT7"); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}
