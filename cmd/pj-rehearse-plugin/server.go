package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/sirupsen/logrus"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/git/v2"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pluginhelp"
)

const (
	rehearsalsAckLabel = "rehearsals-ack"
	needsOkToTestLabel = "needs-ok-to-test"
	rehearseNormal     = "/pj-rehearse"
	rehearseMore       = "/pj-rehearse more"
	rehearseMax        = "/pj-rehearse max"
	rehearseSkip       = "/pj-rehearse skip"
	rehearseAck        = "/pj-rehearse ack"
)

var commentRegex = regexp.MustCompile(`(?m)^/pj-rehearse\s*(.*)$`)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	AddLabel(org, repo string, number int, label string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
}

func helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `The pj-rehearse plugin is used to rehearse changes to affected job configurations.`,
	}
	//TODO: add plugin help
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/pj-rehearse",
		Description: "",
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{"/pj-rehearse"},
	})
	return pluginHelp, nil
}

type server struct {
	ghc githubClient
	gc  git.ClientFactory

	preCheck        bool
	rehearsalConfig rehearsalConfig
}

func serverFromOptions(o options) (*server, error) {
	githubTokenGenerator := secret.GetTokenGenerator(o.github.TokenPath)
	ghc, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		return nil, fmt.Errorf("error creating GitHub client: %v", err)
	}

	gc, err := o.git.GitClient(ghc, githubTokenGenerator, secret.Censor, o.dryRun)
	if err != nil {
		return nil, fmt.Errorf("error creating git client: %v", err)
	}

	return &server{
		ghc:      ghc,
		gc:       gc,
		preCheck: o.preCheck,
		rehearsalConfig: rehearsalConfig{
			prowjobKubeconfig: o.prowjobKubeconfig,
			kubernetesOptions: o.kubernetesOptions,
			noTemplates:       o.noTemplates,
			noRegistry:        o.noRegistry,
			noClusterProfiles: o.noClusterProfiles,
			normalLimit:       o.normalLimit,
			moreLimit:         o.moreLimit,
			maxLimit:          o.maxLimit,
			dryRun:            o.dryRun,
		},
	}, nil
}

func (s *server) handlePullRequestCreation(l *logrus.Entry, event github.PullRequestEvent) {
	if github.PullRequestActionOpened == event.Action {
		org := event.Repo.Owner.Login
		repo := event.Repo.Name
		number := event.Number
		logger := l.WithFields(logrus.Fields{
			"org":  org,
			"repo": repo,
			"pr":   number,
		})
		logger.Debug("handling pull request creation")

		var comment string
		if s.preCheck {
			repoClient, err := s.gc.ClientFor(org, repo)
			if err != nil {
				logger.WithError(err).Error("couldn't get git client")
			}

			defer func() {
				if err := repoClient.Clean(); err != nil {
					logrus.WithError(err).Error("couldn't clean temporary repo folder")
				}
			}()

			if err := repoClient.CheckoutPullRequest(number); err != nil {
				logger.WithError(err).Error("couldn't checkout pull request")
			}

			presubmits, periodics, _, _ := s.rehearsalConfig.determineAffectedJobs(event.PullRequest, repoClient.Directory(), logger)
			if len(presubmits) > 0 || len(periodics) > 0 {
				lines := s.getJobsTableLines(presubmits, periodics, event.PullRequest.User.Login)
				lines = append(lines, []string{
					"Prior to this PR being merged, you will need to either run and acknowledge or opt to skip these rehearsals.",
					"",
				}...)
				lines = append(lines, s.getUsageDetailsLines()...)
				comment = strings.Join(lines, "\n")
			} else {
				comment = fmt.Sprintf("@%s: no rehearsable tests are affected by this change", event.PullRequest.User.Login)
				if err := s.ghc.AddLabel(org, repo, event.Number, rehearsalsAckLabel); err != nil {
					logger.WithError(err).Error("failed to add rehearsals-ack label")
				}
			}
		} else {
			lines := []string{
				fmt.Sprintf("@%s: changes from this PR may affect rehearsable jobs. The `pj-rehearse` plugin is available to rehearse these jobs.", event.PullRequest.User.Login),
				"", // For formatting
			}
			lines = append(lines, s.getUsageDetailsLines()...)
			comment = strings.Join(lines, "\n")
		}

		if err := s.ghc.CreateComment(org, repo, event.Number, comment); err != nil {
			logger.WithError(err).Error("failed to create comment")
		}
	}
}

func (s *server) handleIssueComment(l *logrus.Entry, event github.IssueCommentEvent) {
	if !event.Issue.IsPullRequest() || github.IssueCommentActionCreated != event.Action {
		return
	}

	comment := event.Comment.Body
	pjRehearseComments := commentRegex.FindAllString(comment, -1)
	if len(pjRehearseComments) == 1 { //TODO: do we want to allow multiple interactions in one comment???
		org := event.Repo.Owner.Login
		repo := event.Repo.Name
		number := event.Issue.Number
		logger := l.WithFields(logrus.Fields{
			"org":  org,
			"repo": repo,
			"pr":   number,
		})
		logger.Debugf("handling issue comment: %s", comment)

		pullRequest, err := s.ghc.GetPullRequest(org, repo, number)
		if err != nil {
			logger.WithError(err).Error("failed to get PR for issue comment event")
			// We shouldn't continue here
			return
		}
		// We shouldn't allow rehearsals to run (or be ack'd) on untrusted PRs
		for _, label := range pullRequest.Labels {
			if needsOkToTestLabel == label.Name {
				logger.Infof("%s label found, no rehearsals will be ran", needsOkToTestLabel)
				return
			}
		}

		command := pjRehearseComments[0]
		switch command {
		case rehearseAck, rehearseSkip:
			if err := s.ghc.AddLabel(org, repo, number, rehearsalsAckLabel); err != nil {
				logger.WithError(err).Error("failed to add rehearsals-ack label")
			}
		case rehearseNormal, rehearseMore, rehearseMax:
			rc := s.rehearsalConfig
			repoClient, err := s.gc.ClientFor(org, repo)
			if err != nil {
				logger.WithError(err).Error("couldn't get git client")
			}

			defer func() {
				if err := repoClient.Clean(); err != nil {
					logrus.WithError(err).Error("couldn't clean temporary repo folder")
				}
			}()

			if err := repoClient.CheckoutPullRequest(pullRequest.Number); err != nil {
				logger.WithError(err).Error("couldn't checkout pull request")
			}

			candidate := repoClient.Directory()
			presubmits, periodics, changedTemplates, changedClusterProfiles := rc.determineAffectedJobs(*pullRequest, candidate, logger)
			// Since we didn't provide a comment about the rehearsable jobs prior to the user selecting to run, list them now
			if !s.preCheck {
				jobListComment := strings.Join(s.getJobsTableLines(presubmits, periodics, pullRequest.User.Login), "\n")
				if err := s.ghc.CreateComment(org, repo, number, jobListComment); err != nil {
					logger.WithError(err).Error("failed to create comment")
				}
			}

			if len(presubmits) > 0 || len(periodics) > 0 {
				limit := rc.normalLimit
				if command == rehearseMore {
					limit = rc.moreLimit
				} else if command == rehearseMax {
					limit = rc.maxLimit
				}

				if err = rc.rehearseJobs(*pullRequest, candidate, presubmits, periodics, changedTemplates, changedClusterProfiles, limit, logger); err != nil {
					logrus.WithError(err).Error("couldn't rehearse jobs")
					if err := s.ghc.CreateComment(org, repo, number, "Failed to create rehearsal jobs"); err != nil {
						logger.WithError(err).Error("couldn't create comment")
					}
				}
			}
		}
	}
}

func (s *server) getJobsTableLines(presubmits config.Presubmits, periodics config.Periodics, user string) []string {
	lines := []string{
		fmt.Sprintf("@%s: the following rehearsable tests have been affected by this change:", user),
		"",
		"Test name | Repo | Type | Reason",
		"--- | --- | --- | ---",
	}

	for repoName, jobs := range presubmits {
		for _, presubmit := range jobs {
			lines = append(lines, fmt.Sprintf("%s | %s | %s | %s", presubmit.Name, repoName, "presubmit", config.GetSourceType(presubmit.Labels)))
		}
	}
	for jobName, periodic := range periodics {
		lines = append(lines, fmt.Sprintf("%s | N/A | %s | %s", jobName, "periodic", config.GetSourceType(periodic.Labels)))
	}

	return append(lines, "") // For formatting
}

func (s *server) getUsageDetailsLines() []string {
	rc := s.rehearsalConfig
	return []string{
		"<details>",
		"<summary>Interacting with pj-rehearse</summary>",
		"",
		fmt.Sprintf("Comment: `/pj-rehearse` to run up to %d rehearsals", rc.normalLimit),
		"Comment: `/pj-rehearse skip` to opt-out of rehearsals",
		fmt.Sprintf("Comment: `/pj-rehearse more` to run up to %d rehearsals", rc.moreLimit),
		fmt.Sprintf("Comment: `/pj-rehearse max` to run up to %d rehearsals", rc.maxLimit),
		"",
		fmt.Sprintf("Once you are satisfied with the results of the rehearsals, comment: `/pj-rehearse ack` to unblock merge. When the `%s` label is present on your PR, merge will no longer be blocked by rehearsals", rehearsalsAckLabel),
		"</details>",
	}
}
