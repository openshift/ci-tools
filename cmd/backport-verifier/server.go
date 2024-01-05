package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pluginhelp"
)

type githubClient interface {
	ListPullRequestCommits(org, repo string, number int) ([]github.RepositoryCommit, error)
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)

	CreateComment(owner, repo string, number int, comment string) error

	AddLabel(org, repo string, number int, label string) error
	RemoveLabel(org, repo string, number int, label string) error
}

const (
	validatedBackportsLabel   = "backports/validated-commits"
	unvalidatedBackportsLabel = "backports/unvalidated-commits"
)

var (
	commandRe      = regexp.MustCompile(`(?mi)^/validate-backports\s*$`)
	upstreamPullRe = regexp.MustCompile(`^UPSTREAM: ([0-9]+): `)
)

func helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `The backport validation plugin is used to validate that backports come from merged PRs in a configured upstream repository.`,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/validate-backports",
		Description: "Validate that backports come from merged PRs in the upstream repository",
		WhoCanUse:   "Anyone",
		Examples:    []string{"/validate-backports"},
	})
	return pluginHelp, nil
}

type server struct {
	config func() *Config

	ghc githubClient
}

func (s *server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) {
	if !commandRe.MatchString(ic.Comment.Body) {
		return
	}
	l.Info("Backport validation of PR has been requested.")
	s.handle(l, ic.Repo.Owner.Login, ic.Repo.Name, ic.Comment.User.Login, ic.Issue.Number, true)
}

func (s *server) handlePullRequestEvent(l *logrus.Entry, event github.PullRequestEvent) {
	if event.Action != github.PullRequestActionOpened && event.Action != github.PullRequestActionSynchronize {
		return
	}
	l.Info("Changes to pull request require backport validation")
	s.handle(l, event.Repo.Owner.Login, event.Repo.Name, event.PullRequest.User.Login, event.PullRequest.Number, false)
}

func (s *server) handle(l *logrus.Entry, org, repo, user string, num int, requested bool) {
	logger := l.WithFields(logrus.Fields{
		github.OrgLogField:  org,
		github.RepoLogField: repo,
		github.PrLogField:   num,
	})

	upstream, configured := s.config().Repositories[fmt.Sprintf("%s/%s", org, repo)]
	if !configured {
		if requested {
			if err := s.ghc.CreateComment(org, repo, num, fmt.Sprintf("@%s: no upstream repository is configured for validating backports for this repository.", user)); err != nil {
				logger.WithError(err).Warn("couldn't create comment")
			}
			ensureLabels(s.ghc, l, unvalidatedBackportsLabel, org, repo, num)
		}
		return
	}

	parts := strings.Split(upstream, "/")
	upstreamOrg, upstreamRepo := parts[0], parts[1]

	commits, err := s.ghc.ListPullRequestCommits(org, repo, num)
	if err != nil {
		if commentErr := s.ghc.CreateComment(org, repo, num, fmt.Sprintf(`@%s: could not list commits in this pull request. Please try again with /validate-backports.

<details>

%s

</details>`, user, err)); commentErr != nil {
			logger.WithError(commentErr).Warn("couldn't list commits")
		}
		return
	}

	invalidCommits := map[string]string{}
	validCommits := map[string]string{}
	upstreamPullsByCommit := map[string]int{}
	errorsByCommit := map[string]string{}
	messagesByCommit := map[string]string{}
	for _, commit := range commits {
		messagesByCommit[commit.SHA] = strings.Split(commit.Commit.Message, "\n")[0]
		parts := upstreamPullRe.FindStringSubmatch(commit.Commit.Message)
		if len(parts) != 2 {
			invalidCommits[commit.SHA] = "does not specify an upstream backport in the commit message"
			continue
		}
		pr, err := strconv.Atoi(parts[1])
		if err != nil {
			// based on the regex this should not happen ...
			logger.WithError(err).Warn("Failed to parse PR as integer")
			errorsByCommit[commit.SHA] = fmt.Sprintf("failed to parse PR identifier: %s", err.Error())
			continue
		}
		upstreamPullsByCommit[commit.SHA] = pr
	}

	for commit, pull := range upstreamPullsByCommit {
		prMention := fmt.Sprintf("the upstream PR [%s/%s#%d](https://github.com/%s/%s/pull/%d)", upstreamOrg, upstreamRepo, pull, upstreamOrg, upstreamRepo, pull)
		pr, err := s.ghc.GetPullRequest(upstreamOrg, upstreamRepo, pull)
		if err != nil {
			if !github.IsNotFound(err) {
				logger.WithError(err).Warn("Failed to get upstream PR")
				errorsByCommit[commit] = fmt.Sprintf("failed to fetch upstream PR: %s", err.Error())
				continue
			}
			invalidCommits[commit] = prMention + " does not exist"
			continue
		}
		if !pr.Merged {
			invalidCommits[commit] = prMention + " has not yet merged"
		} else {
			validCommits[commit] = prMention + " has merged"
		}
	}

	desired := unvalidatedBackportsLabel
	verb := "could not"
	if len(errorsByCommit) == 0 && len(invalidCommits) == 0 {
		desired = validatedBackportsLabel
		verb = "could"
	}
	ensureLabels(s.ghc, l, desired, org, repo, num)

	message := fmt.Sprintf("@%s: the contents of this pull request %s be automatically validated.", user, verb)
	for _, item := range []struct {
		qualifier string
		data      map[string]string
	}{
		{"are valid", validCommits},
		{"could not be validated and must be approved by a top-level approver", invalidCommits},
		{"could not be processed", errorsByCommit},
	} {
		if len(item.data) > 0 {
			var formatted []string
			for commit, why := range item.data {
				formatted = append(formatted, fmt.Sprintf(" - [%s|%s](https://github.com/%s/%s/commit/%s): %s", commit[0:7], messagesByCommit[commit], org, repo, commit, why))
			}
			sort.Strings(formatted)
			message = fmt.Sprintf("%s\n\nThe following commits %s:\n%s", message, item.qualifier, strings.Join(formatted, "\n"))
		}
	}
	footer := "\n\nComment <code>/validate-backports</code> to re-evaluate validity of the upstream PRs, for example when they are merged upstream."
	message = message + footer
	if commentErr := s.ghc.CreateComment(org, repo, num, message); commentErr != nil {
		logger.WithError(commentErr).Warn("couldn't respond to user")
	}
}

func ensureLabels(client githubClient, l *logrus.Entry, desired string, org, repo string, num int) {
	var unwanted string
	if desired == validatedBackportsLabel {
		unwanted = unvalidatedBackportsLabel
	} else {
		unwanted = validatedBackportsLabel
	}
	if err := client.AddLabel(org, repo, num, desired); err != nil {
		l.WithError(err).Warn("could not add label", err)
	}
	if err := client.RemoveLabel(org, repo, num, unwanted); err != nil {
		l.WithError(err).Warn("could not remove label", err)
	}
}
