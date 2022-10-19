package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/git/v2"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pluginhelp"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/rehearse"
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
	GetRef(org, repo, ref string) (string, error)
}

type server struct {
	ghc githubClient
	gc  git.ClientFactory

	preCheck        bool
	rehearsalConfig rehearse.RehearsalConfig
}

func (s *server) helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `The pj-rehearse plugin is used to rehearse changes to affected job configurations.`,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseNormal,
		Description: fmt.Sprintf("Run up to %d affected job rehearsals for the change in the PR.", s.rehearsalConfig.NormalLimit),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseNormal},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseAck,
		Description: fmt.Sprintf("Acknowledge the rehearsal result (either passing, failing, or skipped), and add the '%s' label allowing merge once other requirements are met.", rehearsalsAckLabel),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseAck},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseMore,
		Description: fmt.Sprintf("Run up to %d affected job rehearsals for the change in the PR.", s.rehearsalConfig.MoreLimit),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseMore},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseMax,
		Description: fmt.Sprintf("Run up to %d affected job rehearsals for the change in the PR.", s.rehearsalConfig.MaxLimit),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseMax},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseSkip,
		Description: fmt.Sprintf("Opt-out of rehearsals for this PR, and add the '%s' label allowing merge once other requirements are met.", rehearsalsAckLabel),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseSkip},
	})
	return pluginHelp, nil
}

func serverFromOptions(o options) (*server, error) {
	githubTokenGenerator := secret.GetTokenGenerator(o.github.TokenPath)
	ghc, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		return nil, fmt.Errorf("error creating GitHub client: %w", err)
	}

	gc, err := o.git.GitClient(ghc, githubTokenGenerator, secret.Censor, o.dryRun)
	if err != nil {
		return nil, fmt.Errorf("error creating git client: %w", err)
	}

	return &server{
		ghc:             ghc,
		gc:              gc,
		preCheck:        o.preCheck,
		rehearsalConfig: rehearsalConfigFromOptions(o),
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

			candidate, err := s.getCandidate(event.PullRequest)
			if err != nil {
				logger.WithError(err).Error("couldn't get candidate from pull request")
			}
			presubmits, periodics, _, _, err := s.rehearsalConfig.DetermineAffectedJobs(candidate, repoClient.Directory(), logger)
			if err != nil {
				logger.WithError(err).Error("couldn't determine affected jobs")
			}
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
	if len(pjRehearseComments) > 0 {
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

		repoClient, err := s.gc.ClientFor(org, repo)
		if err != nil {
			logger.WithError(err).Error("couldn't get git client")
		}
		defer func() {
			if err := repoClient.Clean(); err != nil {
				logrus.WithError(err).Error("couldn't clean temporary repo folder")
			}
		}()

		rehearsalsTriggered := false
		for _, command := range pjRehearseComments {
			switch command {
			case rehearseAck, rehearseSkip:
				if err := s.ghc.AddLabel(org, repo, number, rehearsalsAckLabel); err != nil {
					logger.WithError(err).Error("failed to add rehearsals-ack label")
				}
			case rehearseNormal, rehearseMore, rehearseMax:
				if rehearsalsTriggered {
					// We don't want to trigger rehearsals more than once per comment
					continue
				}
				rc := s.rehearsalConfig
				if err := repoClient.CheckoutPullRequest(pullRequest.Number); err != nil {
					logger.WithError(err).Error("couldn't checkout pull request")
				}

				candidatePath := repoClient.Directory()
				candidate, err := s.getCandidate(*pullRequest)
				if err != nil {
					logger.WithError(err).Error("couldn't get candidate from pull request")
				}
				presubmits, periodics, changedTemplates, changedClusterProfiles, err := rc.DetermineAffectedJobs(candidate, candidatePath, logger)
				if err != nil {
					logger.WithError(err).Error("couldn't determine affected jobs")
				}
				if !s.preCheck {
					// Since we didn't provide a comment about the rehearsable jobs prior to the user selecting to run, list them now
					jobListComment := strings.Join(s.getJobsTableLines(presubmits, periodics, pullRequest.User.Login), "\n")
					if err := s.ghc.CreateComment(org, repo, number, jobListComment); err != nil {
						logger.WithError(err).Error("failed to create comment")
					}
				}

				if len(presubmits) > 0 || len(periodics) > 0 {
					limit := rc.NormalLimit
					if command == rehearseMore {
						limit = rc.MoreLimit
					} else if command == rehearseMax {
						limit = rc.MaxLimit
					}

					loggers := rehearse.Loggers{Job: logger, Debug: logger} // TODO: for now use the same logger, the dual logger concept will go away when orignal pj-rehearse does
					prConfig, prRefs, imageStreamTags, presubmitsToRehearse, err := rc.SetupJobs(candidate, candidatePath, presubmits, periodics, changedTemplates, changedClusterProfiles, limit, loggers)
					if err != nil {
						logger.WithError(err).Error("couldn't set up jobs")
						if err = s.ghc.CreateComment(org, repo, number, "`pj-rehearse` was unable to set up jobs"); err != nil {
							logger.WithError(err).Error("failed to create comment")
						}
					}

					if err := prConfig.Prow.ValidateJobConfig(); err != nil {
						logger.WithError(err).Error("validation of job config failed")
						if err = s.ghc.CreateComment(org, repo, number, "`pj-rehearse` validation of job config failed"); err != nil {
							logger.WithError(err).Error("failed to create comment")
						}
					}

					_, err = rc.RehearseJobs(candidate, candidatePath, prConfig, prRefs, imageStreamTags, presubmitsToRehearse, changedTemplates, changedClusterProfiles, loggers)
					if err != nil {
						logger.WithError(err).Error("couldn't rehearse jobs")
						if err := s.ghc.CreateComment(org, repo, number, "Failed to create rehearsal jobs"); err != nil {
							logger.WithError(err).Error("couldn't create comment")
						}
					}
				}
				rehearsalsTriggered = true
			}
		}
	}
}

func (s *server) getCandidate(pullRequest github.PullRequest) (rehearse.RehearsalCandidate, error) {
	// We can't just use the base SHA from the pullRequest as there may be changes to the branches head in the meantime
	repo := pullRequest.Base.Repo
	baseSHA, err := s.ghc.GetRef(repo.Owner.Login, repo.Name, fmt.Sprintf("heads/%s", pullRequest.Base.Ref))
	if err != nil {
		return rehearse.RehearsalCandidate{}, err
	}
	return rehearse.RehearsalCandidateFromPullRequest(pullRequest, baseSHA), nil
}

func (s *server) getJobsTableLines(presubmits config.Presubmits, periodics config.Periodics, user string) []string {
	lines := []string{
		fmt.Sprintf("@%s: the following rehearsable tests have been affected by this change:", user),
		"",
		"Test name | Repo | Type | Reason",
		"--- | --- | --- | ---",
	}

	limitToList := s.rehearsalConfig.MaxLimit
	jobCount := 0
	for repoName, jobs := range presubmits {
		for _, presubmit := range jobs {
			jobCount++
			if jobCount < limitToList {
				lines = append(lines, fmt.Sprintf("%s | %s | %s | %s", presubmit.Name, repoName, "presubmit", config.GetSourceType(presubmit.Labels).GetDisplayText()))
			}
		}
	}
	for jobName, periodic := range periodics {
		jobCount++
		if jobCount < limitToList {
			lines = append(lines, fmt.Sprintf("%s | N/A | %s | %s", jobName, "periodic", config.GetSourceType(periodic.Labels).GetDisplayText()))
		}
	}

	if jobCount > limitToList {
		lines = append(lines, "") // For formatting
		lines = append(lines, fmt.Sprintf("A total of %d jobs were affected by this change. The above listing is non-exhaustive and limited to %d jobs", jobCount, limitToList))
	}

	return append(lines, "") // For formatting
}

func (s *server) getUsageDetailsLines() []string {
	rc := s.rehearsalConfig
	return []string{
		"<details>",
		"<summary>Interacting with pj-rehearse</summary>",
		"",
		fmt.Sprintf("Comment: `%s` to run up to %d rehearsals", rehearseNormal, rc.NormalLimit),
		fmt.Sprintf("Comment: `%s` to opt-out of rehearsals", rehearseSkip),
		fmt.Sprintf("Comment: `%s` to run up to %d rehearsals", rehearseMore, rc.MoreLimit),
		fmt.Sprintf("Comment: `%s` to run up to %d rehearsals", rehearseMax, rc.MaxLimit),
		"",
		fmt.Sprintf("Once you are satisfied with the results of the rehearsals, comment: `%s` to unblock merge. When the `%s` label is present on your PR, merge will no longer be blocked by rehearsals", rehearseAck, rehearsalsAckLabel),
		"</details>",
	}
}
