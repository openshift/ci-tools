package main

import (
	"errors"
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
	rehearseReject     = "/pj-rehearse reject"
	rehearseRefresh    = "/pj-rehearse refresh"
	rehearseAutoAck    = "/pj-rehearse auto-ack"
)

var commentRegex = regexp.MustCompile(`(?m)^/pj-rehearse\f*.*$`)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	AddLabel(org, repo string, number int, label string) error
	RemoveLabel(org, repo string, number int, label string) error
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
		Usage:       rehearseRefresh,
		Description: "Request an updated list of affected jobs. Useful when there is a new push to the branch.",
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseRefresh},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseSkip,
		Description: fmt.Sprintf("Opt-out of rehearsals for this PR, and add the '%s' label allowing merge once other requirements are met.", rehearsalsAckLabel),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseSkip},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseReject,
		Description: fmt.Sprintf("Un-acknowledge the rehearsals and remove the '%s' label blocking merge until it is added back.", rehearsalsAckLabel),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseReject},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseAutoAck,
		Description: fmt.Sprintf("Run up to %d affected job rehearsals for the change in the PR, and add the '%s' label on success.", s.rehearsalConfig.NormalLimit, rehearsalsAckLabel),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseAutoAck},
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
		user := event.PullRequest.User.Login
		logger := l.WithFields(logrus.Fields{
			"org":  org,
			"repo": repo,
			"pr":   number,
		})
		logger.Debug("handling pull request creation")
		pullRequest := &event.PullRequest
		s.respondToNewPR(pullRequest, logger)
		s.handlePotentialCommands(pullRequest, event.PullRequest.Body, user, logger)
	}
}

func (s *server) respondToNewPR(pullRequest *github.PullRequest, logger *logrus.Entry) {
	org := pullRequest.Base.Repo.Owner.Login
	repo := pullRequest.Base.Repo.Name
	number := pullRequest.Number
	user := pullRequest.User.Login
	var comment string
	if s.preCheck {
		presubmits, periodics, _, _, err := s.getAffectedJobs(pullRequest, logger)
		if err != nil {
			if err := s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s, pj-rehearse couldn't determine affected jobs. This could be due to a branch that needs to be rebased.", user)); err != nil {
				logger.WithError(err).Error("failed to create comment")
			}
		}
		foundJobsToRehearse := len(presubmits) > 0 || len(periodics) > 0
		if !foundJobsToRehearse {
			s.acknowledgeRehearsals(org, repo, number, logger)
		}

		lines := s.getJobsTableLines(presubmits, periodics, user)
		if foundJobsToRehearse {
			lines = append(lines, []string{
				"Prior to this PR being merged, you will need to either run and acknowledge or opt to skip these rehearsals.",
				"",
			}...)
			lines = append(lines, s.getUsageDetailsLines()...)
		}
		comment = strings.Join(lines, "\n")
	} else {
		lines := []string{
			fmt.Sprintf("@%s: changes from this PR may affect rehearsable jobs. The `pj-rehearse` plugin is available to rehearse these jobs.", user),
			"", // For formatting
		}
		lines = append(lines, s.getUsageDetailsLines()...)
		comment = strings.Join(lines, "\n")
	}

	if err := s.ghc.CreateComment(org, repo, number, comment); err != nil {
		logger.WithError(err).Error("failed to create comment")
	}
}

func (s *server) handleIssueComment(l *logrus.Entry, event github.IssueCommentEvent) {
	if !event.Issue.IsPullRequest() || github.IssueCommentActionCreated != event.Action {
		return
	}
	org := event.Repo.Owner.Login
	repo := event.Repo.Name
	number := event.Issue.Number
	logger := l.WithFields(logrus.Fields{
		"org":  org,
		"repo": repo,
		"pr":   number,
	})
	comment := event.Comment.Body
	pullRequest, err := s.ghc.GetPullRequest(org, repo, number)
	if err != nil {
		logger.WithError(err).Error("failed to get PR for issue comment event")
		// We shouldn't continue here
		return
	}
	s.handlePotentialCommands(pullRequest, comment, event.Comment.User.Login, logger)
}

func (s *server) handlePotentialCommands(pullRequest *github.PullRequest, comment, user string, logger *logrus.Entry) {
	pjRehearseComments := commentRegex.FindAllString(comment, -1)
	if len(pjRehearseComments) > 0 {
		logger.Debugf("handling commands: %s", comment)
		org := pullRequest.Base.Repo.Owner.Login
		repo := pullRequest.Base.Repo.Name
		number := pullRequest.Number

		// We shouldn't allow rehearsals to run (or be ack'd) on untrusted PRs
		for _, label := range pullRequest.Labels {
			if needsOkToTestLabel == label.Name {
				logger.Infof("%s label found, no rehearsals will be ran", needsOkToTestLabel)
				return
			}
		}

		rehearsalsTriggered := false
		for _, command := range pjRehearseComments {
			command = strings.TrimSpace(command)
			logger.Debugf("handling command: %s", command)
			switch command {
			case rehearseAck, rehearseSkip:
				s.acknowledgeRehearsals(org, repo, number, logger)
			case rehearseReject:
				if err := s.ghc.RemoveLabel(org, repo, number, rehearsalsAckLabel); err != nil {
					logger.WithError(err).Errorf("failed to remove '%s' label", rehearsalsAckLabel)
				}
			case rehearseRefresh:
				presubmits, periodics, _, _, err := s.getAffectedJobs(pullRequest, logger)
				if err != nil {
					if err := s.ghc.CreateComment(org, repo, pullRequest.Number, fmt.Sprintf("@%s, pj-rehearse couldn't determine affected jobs. This could be due to a branch that needs to be rebased.", user)); err != nil {
						logger.WithError(err).Error("failed to create comment")
					}
				}
				foundJobsToRehearse := len(presubmits) > 0 || len(periodics) > 0
				if !foundJobsToRehearse {
					s.acknowledgeRehearsals(org, repo, number, logger)
				}
				comment = strings.Join(s.getJobsTableLines(presubmits, periodics, user), "\n")
				if err := s.ghc.CreateComment(org, repo, number, comment); err != nil {
					logger.WithError(err).Error("failed to create comment")
				}
			case rehearseNormal, rehearseMore, rehearseMax, rehearseAutoAck:
				if rehearsalsTriggered {
					// We don't want to trigger rehearsals more than once per comment
					continue
				}
				rehearsalsTriggered = true

				rc := s.rehearsalConfig
				repoClient, err := s.getRepoClient(org, repo)
				if err != nil {
					logger.WithError(err).Error("couldn't create repo client")
				}
				defer func() {
					if err := repoClient.Clean(); err != nil {
						logrus.WithError(err).Error("couldn't clean temporary repo folder")
					}
				}()

				candidate, err := s.prepareCandidate(repoClient, pullRequest)
				if err != nil {
					if err := s.ghc.CreateComment(org, repo, number, fmt.Sprintf("pj-rehearse couldn't prepare a candidate for rehearsal; rehearsals will not be run. This could be due to a branch that needs to be rebased.")); err != nil {
						logger.WithError(err).Error("failed to create comment")
					}
					continue
				}

				//TODO(DPTP-2888): this is the point at which we can use repoClient.RevParse() to see if we even need to load the configs at all, and also prune the set of loaded configs to only the changed files

				candidatePath := repoClient.Directory()
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
						s.reportFailure("unable to set up jobs", org, repo, user, number, logger)
					}

					if err := prConfig.Prow.ValidateJobConfig(); err != nil {
						logger.WithError(err).Error("validation of job config failed")
						s.reportFailure("config validation failed", org, repo, user, number, logger)
					}

					success, err := rc.RehearseJobs(candidate, candidatePath, prConfig, prRefs, imageStreamTags, presubmitsToRehearse, changedTemplates, changedClusterProfiles, loggers)
					if err != nil {
						logger.WithError(err).Error("couldn't rehearse jobs")
						s.reportFailure("failed to create rehearsal jobs", org, repo, user, number, logger)
					}

					autoAckMode := rehearseAutoAck == command
					if autoAckMode && success {
						s.acknowledgeRehearsals(org, repo, number, logger)
					}
				} else {
					s.acknowledgeRehearsals(org, repo, number, logger)
					if err = s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s: no rehearsable tests are affected by this change", user)); err != nil {
						logger.WithError(err).Error("failed to create comment")
					}
				}
			}
		}
	}
}

func (s *server) getAffectedJobs(pullRequest *github.PullRequest, logger *logrus.Entry) (config.Presubmits, config.Periodics, *rehearse.ConfigMaps, *rehearse.ConfigMaps, error) {
	rc := s.rehearsalConfig
	org := pullRequest.Base.Repo.Owner.Login
	repo := pullRequest.Base.Repo.Name
	repoClient, err := s.getRepoClient(org, repo)
	if err != nil {
		logger.WithError(err).Error("couldn't create repo client")
		return nil, nil, nil, nil, fmt.Errorf("couldn't create repo client: %w", err)
	}
	defer func() {
		if err := repoClient.Clean(); err != nil {
			logrus.WithError(err).Error("couldn't clean temporary repo folder")
		}
	}()

	candidate, err := s.prepareCandidate(repoClient, pullRequest)
	if err != nil {
		logger.WithError(err).Error("couldn't prepare candidate")
		return nil, nil, nil, nil, fmt.Errorf("couldn't prepare candidate: %w", err)
	}

	//TODO(DPTP-2888): this is the point at which we can use repoClient.RevParse() to see if we even need to load the configs at all, and also prune the set of loaded configs to only the changed files

	candidatePath := repoClient.Directory()
	return rc.DetermineAffectedJobs(candidate, candidatePath, logger)
}

func (s *server) reportFailure(message, org, repo, user string, number int, l *logrus.Entry) {
	if err := s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s, `pj-rehearse`: %s", user, message)); err != nil {
		l.WithError(err).Error("failed to create comment")
	}
}

func (s *server) getRepoClient(org, repo string) (git.RepoClient, error) {
	repoClient, err := s.gc.ClientFor(org, repo)
	if err != nil {
		return nil, fmt.Errorf("couldn't get git client: %w", err)
	}
	if err := repoClient.Config("user.name", "prow"); err != nil {
		return nil, fmt.Errorf("couldn't set user.name in git client: %w", err)
	}
	if err := repoClient.Config("user.email", "prow@localhost"); err != nil {
		return nil, fmt.Errorf("couldn't set user.email in git client: %w", err)
	}

	return repoClient, nil
}

func (s *server) prepareCandidate(repoClient git.RepoClient, pullRequest *github.PullRequest) (rehearse.RehearsalCandidate, error) {
	if err := repoClient.CheckoutPullRequest(pullRequest.Number); err != nil {
		return rehearse.RehearsalCandidate{}, fmt.Errorf("couldn't checkout pull request: %w", err)
	}

	repo := pullRequest.Base.Repo
	baseSHA, err := s.ghc.GetRef(repo.Owner.Login, repo.Name, fmt.Sprintf("heads/%s", pullRequest.Base.Ref))
	if err != nil {
		return rehearse.RehearsalCandidate{}, fmt.Errorf("couldn't get ref: %w", err)
	}
	candidate := rehearse.RehearsalCandidateFromPullRequest(pullRequest, baseSHA)

	// In order to determine *only* the affected jobs from the changes in the PR, we need to rebase onto master
	baseRef := pullRequest.Base.Ref
	if rebased, _ := repoClient.MergeWithStrategy(baseRef, "rebase"); !rebased {
		return rehearse.RehearsalCandidate{}, errors.New("couldn't rebase repo client")
	}

	return candidate, nil
}

func (s *server) getJobsTableLines(presubmits config.Presubmits, periodics config.Periodics, user string) []string {
	if len(presubmits) == 0 && len(periodics) == 0 {
		return []string{fmt.Sprintf("@%s: no rehearsable tests are affected by this change", user)}
	}

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
		lines = append(lines, fmt.Sprintf("A total of %d jobs have been affected by this change. The above listing is non-exhaustive and limited to %d jobs.", jobCount, limitToList))
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
		fmt.Sprintf("Comment: `%s` to run up to %d rehearsals, and add the `%s` label on success", rehearseAutoAck, rc.NormalLimit, rehearsalsAckLabel),
		fmt.Sprintf("Comment: `%s` to get an updated list of affected jobs (useful if you have new pushes to the branch)", rehearseRefresh),
		"",
		fmt.Sprintf("Once you are satisfied with the results of the rehearsals, comment: `%s` to unblock merge. When the `%s` label is present on your PR, merge will no longer be blocked by rehearsals.", rehearseAck, rehearsalsAckLabel),
		fmt.Sprintf("If you would like the `%s` label removed, comment: `%s` to re-block merging.", rehearsalsAckLabel, rehearseReject),
		"</details>",
	}
}

func (s *server) acknowledgeRehearsals(org, repo string, number int, logger *logrus.Entry) {
	if err := s.ghc.AddLabel(org, repo, number, rehearsalsAckLabel); err != nil {
		logger.WithError(err).Errorf("failed to add '%s' label", rehearsalsAckLabel)
	}
}
