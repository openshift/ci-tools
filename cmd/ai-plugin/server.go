package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
)

const (
	aiPrefix        = "/ai"
	aiReview        = "/ai review"
	aiPrDescription = "/ai pr_description"
	aiCommitMessage = "/ai commit_message"
)

type githubClient interface {
	IsMember(org, user string) (bool, error)
	GetPullRequestDiff(org, repo string, number int) ([]byte, error)
	// UpdatePullRequest(org, repo string, number int, title, body *string, open *bool, branch *string, canModify *bool) error
	CreateComment(owner, repo string, number int, comment string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
}

func helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `The ai plugin is used to interact with an AI service to generate pull request reviews, descriptions, and commit messages.`,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       aiReview,
		Description: "Request a pull request review from an AI.",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{aiReview},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       aiPrDescription,
		Description: "Request a description for a pull request from an AI.",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{aiPrDescription},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       aiCommitMessage,
		Description: "Request a commit message from an AI.",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{aiCommitMessage},
	})
	return pluginHelp, nil
}

type server struct {
	ghc   githubClient
	aiURL string
	dry   bool
}

func (s *server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) {
	l.Infof("Entering handleIssueComment for PR %d in repo %s/%s", ic.Issue.Number, ic.Repo.Owner.Login, ic.Repo.Name)
	if !ic.Issue.IsPullRequest() || !strings.HasPrefix(ic.Comment.Body, aiPrefix) {
		l.Infof("Ignoring issue comment event: PR %d in repo %s/%s is not open or does not match the AI command regex", ic.Issue.Number, ic.Repo.Owner.Login, ic.Repo.Name)
		return
	}
	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	number := ic.Issue.Number
	logger := l.WithFields(logrus.Fields{
		"org":  org,
		"repo": repo,
		"pr":   number,
	})
	logger.Infof("Handling issue comment event for PR %d in repo %s/%s", number, org, repo)
	pullRequest, err := s.ghc.GetPullRequest(org, repo, number)
	if err != nil {
		logger.WithError(err).Error("failed to get PR for issue comment event")
		return
	}
	err = s.checkMember(ic, logger)
	if err != nil {
		logger.WithError(err)
		return
	}
	s.handle(pullRequest, ic, logger)
}

func (s *server) handle(pullRequest *github.PullRequest, ic github.IssueCommentEvent, logger *logrus.Entry) {
	if !s.isAIRunning(logger) {
		logger.Warn("AI service is not running, skipping request")
		return
	}
	diffBytes, err := s.ghc.GetPullRequestDiff(pullRequest.Base.Repo.Owner.Login, pullRequest.Base.Repo.Name, pullRequest.Number)
	if err != nil {
		logger.WithError(err).Error("failed to get pull request diff")
		return
	}
	var response string
	switch ic.Comment.Body {
	case aiReview:
		response, err = s.aiRequest(diffBytes, "/review")
	case aiPrDescription:
		response, err = s.aiRequest(diffBytes, "/pr_description")
	case aiCommitMessage:
		response, err = s.aiRequest(diffBytes, "/commit_message")
	default:
		response, err = s.aiRequest(diffBytes, "/review")
	}
	if s.dry {
		logger.Infof("Dry run, ai response: %s", response)
		return
	}
	if err != nil {
		logger.WithError(err).Error("failed to get AI review response")
		s.createComment(ic, fmt.Sprintf(" failed to get AI review response: %v", err), logger)
		return
	}
	s.createComment(ic, fmt.Sprintf(" %s", response), logger)
}

func (s *server) isAIRunning(logger *logrus.Entry) bool {
	req, err := http.NewRequest("GET", s.aiURL, nil)
	if err != nil {
		logger.WithError(err).Warn("failed to create request to check if AI service is running")
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.WithError(err).Warn("failed to check if AI service is running")
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.Warnf("AI service is not running, received status code: %d", resp.StatusCode)
		return false
	}
	logger.Info("AI service is running")
	return true
}

func (s *server) aiRequest(diff []byte, endpoint string) (string, error) {
	type diffPayload struct {
		Diff string `json:"diff"`
	}
	payload := diffPayload{Diff: string(diff)}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := postWithRetry(s.aiURL+endpoint, bytes.NewBuffer(jsonBody), 3, 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to post to AI service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI service returned non-OK status code: %d", resp.StatusCode)
	}

	type aiResponse struct {
		Response string `json:"response"`
	}
	var result aiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode AI response: %w", err)
	}
	return result.Response, nil
}

func postWithRetry(url string, body io.Reader, maxRetries int, delay time.Duration) (*http.Response, error) {
	for range maxRetries {
		req, err := http.NewRequest("POST", url, body)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to send request: %w", err)
		}
		if resp.StatusCode < 500 {
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(delay)
	}
	return nil, fmt.Errorf("failed to get a successful response after %d retries", maxRetries)
}

func (s *server) checkMember(ic github.IssueCommentEvent, logger *logrus.Entry) error {
	logger.Infof("checking if user %s is a member of the openshift org", ic.Comment.User.Login)
	user := ic.Comment.User.Login
	member, err := s.ghc.IsMember("openshift", user)
	if err != nil {
		logger.WithError(err).Warn("failed to check if user is a member of the openshift org")
		return err
	}
	if !member {
		logger.Infof("user %s is not a member of the openshift org", user)
		message := "You are not allowed to work with the AI plugin. This must be done by a member of the `openshift` org"
		s.createComment(ic, message, logger)
		return fmt.Errorf("user %s is not a member of the openshift org", user)
	}
	return nil
}

func (s *server) createComment(ic github.IssueCommentEvent, message string, logger *logrus.Entry) {
	if err := s.ghc.CreateComment(ic.Repo.Owner.Login, ic.Repo.Name, ic.Issue.Number, fmt.Sprintf("@%s: %s", ic.Comment.User.Login, message)); err != nil {
		logger.WithError(err).Warn("failed to create comment")
	}
}
