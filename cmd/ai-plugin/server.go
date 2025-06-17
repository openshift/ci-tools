package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"

	"github.com/openshift/ci-tools/cmd/ai-plugin/provider"
)

const (
	aiPrefix        = "/ai"
	aiReview        = "/ai review"
	aiPrDescription = "/ai pr_description"
	aiCommitMessage = "/ai commit_message"

	aiReviewText = `You are a senior software engineer and I want you to review a pull request.
	You will be given a diff of the changes made in the pull request. Be thorough in your review
	and provide constructive feedback. But don't be very picky. Try to be concise (ca 400 characters).`
	aiPrDescriptionText = `You are a senior software engineer and I want you to write a description for a pull request.
	You will be given the diff of the changes made in the pull request. Be concise (ca 200 characters) and focus on the key points.`
	aiCommitMessageText = `You are a senior software engineer and I want you to write a concise (ca 50 characters)
	and descriptive commit message. You will be given the diff of the changes made in the pull request. Use the conventional commit format.`
)

type githubClient interface {
	IsMember(org, user string) (bool, error)
	GetPullRequestDiff(org, repo string, number int) ([]byte, error)
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
	ghc      githubClient
	aiURL    string
	aiToken  string
	provider provider.Provider
	dry      bool
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
	diffBytes, err := s.ghc.GetPullRequestDiff(pullRequest.Base.Repo.Owner.Login, pullRequest.Base.Repo.Name, pullRequest.Number)
	if err != nil {
		logger.WithError(err).Error("failed to get pull request diff")
		return
	}
	var response string
	switch ic.Comment.Body {
	case aiReview:
		response, err = s.aiRequest(aiReviewText, diffBytes)
	case aiPrDescription:
		response, err = s.aiRequest(aiPrDescriptionText, diffBytes)
	case aiCommitMessage:
		response, err = s.aiRequest(aiCommitMessageText, diffBytes)
	default:
		response, err = s.aiRequest(aiReviewText, diffBytes)
	}
	if s.dry {
		logger.Infof("Dry run, ai response: %s", response)
		return
	}
	if err != nil {
		logger.WithError(err).Error("failed to get AI review response")
		s.createComment(ic, fmt.Sprintf(" failed to get AI review response: %v", err), logger, s.dry)
		return
	}
	s.createComment(ic, fmt.Sprintf(" %s", response), logger, s.dry)
}

func (s *server) aiRequest(text string, diff []byte) (string, error) {
	req, err := s.provider.GetRequest(s.aiURL, s.aiToken, text, diff)
	if err != nil {
		return "", fmt.Errorf("failed to create AI request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send AI request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI service returned non-OK status code: %d", resp.StatusCode)
	}
	return s.provider.GetResponse(resp)
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
		s.createComment(ic, message, logger, s.dry)
		return fmt.Errorf("user %s is not a member of the openshift org", user)
	}
	return nil
}

func (s *server) createComment(ic github.IssueCommentEvent, message string, logger *logrus.Entry, dryRun bool) {
	if dryRun {
		logger.Infof("Dry run: %s", message)
		return
	}
	if err := s.ghc.CreateComment(ic.Repo.Owner.Login, ic.Repo.Name, ic.Issue.Number, fmt.Sprintf("@%s:\n %s", ic.Comment.User.Login, message)); err != nil {
		logger.WithError(err).Warn("failed to create comment")
	}
}
