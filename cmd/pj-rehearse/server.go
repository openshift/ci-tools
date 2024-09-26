package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/git/v2"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/pod-utils/gcs"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/rehearse"
)

const (
	rehearsalNotifier  = "[REHEARSALNOTIFIER]"
	pjRehearse         = "pj-rehearse"
	needsOkToTestLabel = "needs-ok-to-test"
	rehearseNormal     = "/pj-rehearse"
	rehearseMore       = "/pj-rehearse more"
	rehearseMax        = "/pj-rehearse max"
	rehearseSkip       = "/pj-rehearse skip"
	rehearseAck        = "/pj-rehearse ack"
	rehearseReject     = "/pj-rehearse reject"
	rehearseAutoAck    = "/pj-rehearse auto-ack"
	rehearseAbort      = "/pj-rehearse abort"
)

var commentRegex = regexp.MustCompile(`(?m)^/pj-rehearse\f*.*$`)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	AddLabel(org, repo string, number int, label string) error
	RemoveLabel(org, repo string, number int, label string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetRef(org, repo, ref string) (string, error)
	ListIssueComments(org, repo string, number int) ([]github.IssueComment, error)
	DeleteComment(org, repo string, id int) error
}

type server struct {
	ghc githubClient
	gc  git.ClientFactory

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
		Usage:       fmt.Sprintf("%s {test-name}", rehearseNormal),
		Description: "Run one or more specific rehearsals",
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{fmt.Sprintf("%s {some-test} {another-test}", rehearseNormal)},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseAck,
		Description: fmt.Sprintf("Acknowledge the rehearsal result (either passing, failing, or skipped), and add the '%s' label allowing merge once other requirements are met.", rehearse.RehearsalsAckLabel),
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
		Description: fmt.Sprintf("Opt-out of rehearsals for this PR, and add the '%s' label allowing merge once other requirements are met.", rehearse.RehearsalsAckLabel),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseSkip},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseReject,
		Description: fmt.Sprintf("Un-acknowledge the rehearsals and remove the '%s' label blocking merge until it is added back.", rehearse.RehearsalsAckLabel),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseReject},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseAutoAck,
		Description: fmt.Sprintf("Run up to %d affected job rehearsals for the change in the PR, and add the '%s' label on success.", s.rehearsalConfig.NormalLimit, rehearse.RehearsalsAckLabel),
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseAutoAck},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       rehearseAbort,
		Description: "Abort all active rehearsal jobs for the PR",
		WhoCanUse:   "Anyone can use on trusted PRs",
		Examples:    []string{rehearseAbort},
	})
	return pluginHelp, nil
}

func serverFromOptions(o options) (*server, error) {
	ghc, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		return nil, fmt.Errorf("error creating GitHub client: %w", err)
	}

	gc, err := o.github.GitClientFactory("", &o.config.InRepoConfigCacheDirBase, o.dryRun, false)
	if err != nil {
		return nil, fmt.Errorf("error creating git client: %w", err)
	}

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		return nil, fmt.Errorf("error creating configAgent: %w", err)
	}
	c := configAgent.Config()

	rehearsalConfig := rehearsalConfigFromOptions(o)
	rehearsalConfig.ProwjobNamespace = c.ProwJobNamespace
	rehearsalConfig.PodNamespace = c.PodNamespace

	return &server{
		ghc:             ghc,
		gc:              gc,
		rehearsalConfig: rehearsalConfig,
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
	presubmits, periodics, disabledDueToNetworkAccessToggle, err := s.getAffectedJobs(pullRequest, logger)
	if err != nil {
		comment := "unable to determine affected jobs. This could be due to a branch that needs to be rebased."
		s.reportFailure(comment, err, org, repo, user, number, false, true, logger)
		return
	}
	foundJobsToRehearse := len(presubmits) > 0 || len(periodics) > 0
	if !foundJobsToRehearse {
		s.acknowledgeRehearsals(org, repo, number, logger)
	}

	lines, jobCount := s.getJobsTableLines(presubmits, periodics, user)
	lines = append(lines, s.getDisabledRehearsalsLines(disabledDueToNetworkAccessToggle)...)
	if foundJobsToRehearse {
		if jobCount > s.rehearsalConfig.MaxLimit {
			fileLocation := s.dumpAffectedJobsToGCS(pullRequest, presubmits, periodics, jobCount, logger)
			lines = append(lines, fmt.Sprintf("A full list of affected jobs can be found [here](%s%s)", s.rehearsalConfig.GCSBrowserPrefix, fileLocation))
		}
		lines = append(lines, []string{
			"Prior to this PR being merged, you will need to either run and acknowledge or opt to skip these rehearsals.",
			"",
		}...)
		lines = append(lines, s.getUsageDetailsLines()...)
	}
	comment := strings.Join(lines, "\n")
	if err := s.ghc.CreateComment(org, repo, number, comment); err != nil {
		logger.WithError(err).Error("failed to create comment")
	}
}

func (s *server) handleNewPush(l *logrus.Entry, event github.PullRequestEvent) {
	if github.PullRequestActionSynchronize == event.Action {
		org := event.Repo.Owner.Login
		repo := event.Repo.Name
		number := event.PullRequest.Number
		logger := l.WithFields(logrus.Fields{
			"org":  org,
			"repo": repo,
			"pr":   number,
		})
		logger.Debug("handling new push")
		pullRequest, err := s.ghc.GetPullRequest(org, repo, number)
		if err != nil {
			// This should only happen under GitHub api degradation
			logger.WithError(err).Error("failed to get pull request")
			return
		}

		comments, err := s.ghc.ListIssueComments(org, repo, number)
		if err != nil {
			// This also shouldn't happen, but if it does just log and continue it doesn't affect the rest of the process
			logger.WithError(err).Error("failed to get comments for pull request")
		}
		for _, comment := range comments {
			if strings.HasPrefix(comment.Body, rehearsalNotifier) {
				logger.Debugf("found %s in comment...deleting", rehearsalNotifier)
				if err := s.ghc.DeleteComment(org, repo, comment.ID); err != nil {
					logger.WithError(err).Error("error deleting comment")
				}
			}
		}

		presubmits, periodics, disabledDueToNetworkAccessToggle, err := s.getAffectedJobs(pullRequest, logger)
		user := pullRequest.User.Login
		if err != nil {
			comment := "unable to determine affected jobs. This could be due to a branch that needs to be rebased."
			s.reportFailure(comment, err, org, repo, user, number, false, true, logger)
			return
		}
		foundJobsToRehearse := len(presubmits) > 0 || len(periodics) > 0
		if foundJobsToRehearse {
			if !s.rehearsalConfig.StickyLabelAuthors.Has(event.PullRequest.User.Login) {
				if err := s.ghc.RemoveLabel(org, repo, number, rehearse.RehearsalsAckLabel); err != nil {
					// We shouldn't get an error here if the label doesn't exist, so any error is legitimate
					logger.WithError(err).Errorf("failed to remove '%s' label", rehearse.RehearsalsAckLabel)
				}
			}
		} else {
			s.acknowledgeRehearsals(org, repo, number, logger)
		}
		jobTableLines, jobCount := s.getJobsTableLines(presubmits, periodics, user)
		if jobCount > s.rehearsalConfig.MaxLimit {
			fileLocation := s.dumpAffectedJobsToGCS(pullRequest, presubmits, periodics, jobCount, logger)
			jobTableLines = append(jobTableLines, fmt.Sprintf("A full list of affected jobs can be found [here](%s%s)", s.rehearsalConfig.GCSBrowserPrefix, fileLocation))
		}
		jobTableLines = append(jobTableLines, s.getDisabledRehearsalsLines(disabledDueToNetworkAccessToggle)...)
		jobTableLines = append(jobTableLines, s.getUsageDetailsLines()...)
		if err := s.ghc.CreateComment(org, repo, number, strings.Join(jobTableLines, "\n")); err != nil {
			logger.WithError(err).Error("failed to create comment")
		}
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

		// Sometimes, hooks or requests are dropped causing confusion to the command issuer. We can acknowledge that the request has been received
		if err := s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s: now processing your pj-rehearse request. Please allow up to 10 minutes for jobs to trigger or cancel.", user)); err != nil {
			logger.WithError(err).Error("failed to create acknowledgement comment")
		}

		// We shouldn't allow rehearsals to run (or be ack'd) on untrusted PRs
		for _, label := range pullRequest.Labels {
			if needsOkToTestLabel == label.Name {
				message := fmt.Sprintf("@%s: %s label found, no rehearsals will be run", user, needsOkToTestLabel)
				if err := s.ghc.CreateComment(org, repo, number, message); err != nil {
					logger.WithError(err).Error("failed to create comment")
				}
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
				if err := s.ghc.RemoveLabel(org, repo, number, rehearse.RehearsalsAckLabel); err != nil {
					logger.WithError(err).Errorf("failed to remove '%s' label", rehearse.RehearsalsAckLabel)
				}
			case rehearseAbort:
				s.rehearsalConfig.AbortAllRehearsalJobs(org, repo, number, logger)
			default:
				if rehearsalsTriggered {
					message := fmt.Sprintf("@%s: requesting more than one rehearsal in one comment is not supported. If you would like to rehearse multiple specific jobs, please separate the job names by a space in a single command.", user)
					if err := s.ghc.CreateComment(org, repo, number, message); err != nil {
						logger.WithError(err).Error("failed to create comment")
					}
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
					s.reportFailure("unable prepare a candidate for rehearsal; rehearsals will not be run. This could be due to a branch that needs to be rebased.", err, org, repo, user, number, false, false, logger)
					continue
				}

				//TODO(DPTP-2888): this is the point at which we can use repoClient.RevParse() to see if we even need to load the configs at all, and also prune the set of loaded configs to only the changed files

				candidatePath := repoClient.Directory()
				presubmits, periodics, changedTemplates, changedClusterProfiles, _, err := rc.DetermineAffectedJobs(candidate, candidatePath, logger)
				if err != nil {
					logger.WithError(err).Error("couldn't determine affected jobs")
					s.reportFailure("unable to determine affected jobs", err, org, repo, user, number, true, false, logger)
					continue
				}
				requestedOnly := command != rehearseNormal && command != rehearseMore && command != rehearseMax && command != rehearseAutoAck

				if requestedOnly {
					rawJobs := strings.TrimPrefix(command, rehearseNormal+" ")
					requestedJobs := strings.Split(rawJobs, " ")
					var unaffected []string
					presubmits, periodics, unaffected = rehearse.FilterJobsByRequested(requestedJobs, presubmits, periodics, logger)
					if len(unaffected) > 0 {
						message := fmt.Sprintf("@%s: job(s): %s either don't exist or were not found to be affected, and cannot be rehearsed", user, strings.Join(unaffected, ", "))
						if err := s.ghc.CreateComment(org, repo, number, message); err != nil {
							logger.WithError(err).Error("failed to create comment")
						}
					}
				}
				if len(presubmits) > 0 || len(periodics) > 0 {
					limit := math.MaxInt
					if command == rehearseNormal || command == rehearseAutoAck {
						limit = rc.NormalLimit
					} else if command == rehearseMore {
						limit = rc.MoreLimit
					} else if command == rehearseMax {
						limit = rc.MaxLimit
					}

					prConfig, prRefs, presubmitsToRehearse, err := rc.SetupJobs(candidate, candidatePath, presubmits, periodics, changedTemplates, changedClusterProfiles, limit, logger)
					if err != nil {
						logger.WithError(err).Error("couldn't set up jobs")
						s.reportFailure("unable to set up jobs", err, org, repo, user, number, true, false, logger)
						continue
					}

					if err := prConfig.Prow.ValidateJobConfig(); err != nil {
						logger.WithError(err).Error("validation of job config failed")
						s.reportFailure("config validation failed", err, org, repo, user, number, false, false, logger)
						continue
					}

					autoAckMode := rehearseAutoAck == command
					success, err := rc.RehearseJobs(candidate, candidatePath, prRefs, presubmitsToRehearse, changedTemplates, changedClusterProfiles, prConfig.Prow, autoAckMode, logger)
					if err != nil {
						logger.WithError(err).Error("couldn't rehearse jobs")
						s.reportFailure("failed to create rehearsal jobs", err, org, repo, user, number, true, false, logger)
						continue
					}
					if autoAckMode && success {
						s.acknowledgeRehearsals(org, repo, number, logger)
					}
				} else if !requestedOnly {
					s.acknowledgeRehearsals(org, repo, number, logger)
					if err := s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s: no rehearsable tests are affected by this change", user)); err != nil {
						logger.WithError(err).Error("failed to create comment")
					}
				}
			}
		}
	}
}

func (s *server) getAffectedJobs(pullRequest *github.PullRequest, logger *logrus.Entry) (config.Presubmits, config.Periodics, []string, error) {
	rc := s.rehearsalConfig
	org := pullRequest.Base.Repo.Owner.Login
	repo := pullRequest.Base.Repo.Name
	repoClient, err := s.getRepoClient(org, repo)
	if err != nil {
		logger.WithError(err).Error("couldn't create repo client")
		return nil, nil, nil, fmt.Errorf("couldn't create repo client: %w", err)
	}
	defer func() {
		if err := repoClient.Clean(); err != nil {
			logrus.WithError(err).Error("couldn't clean temporary repo folder")
		}
	}()

	candidate, err := s.prepareCandidate(repoClient, pullRequest)
	if err != nil {
		logger.WithError(err).Error("couldn't prepare candidate")
		return nil, nil, nil, fmt.Errorf("couldn't prepare candidate: %w", err)
	}

	//TODO(DPTP-2888): this is the point at which we can use repoClient.RevParse() to see if we even need to load the configs at all, and also prune the set of loaded configs to only the changed files

	candidatePath := repoClient.Directory()
	presubmits, periodics, _, _, disabledDueToNetworkAccessToggle, err := rc.DetermineAffectedJobs(candidate, candidatePath, logger)
	return presubmits, periodics, disabledDueToNetworkAccessToggle, err
}

func (s *server) reportFailure(message string, err error, org, repo, user string, number int, addContact, addUsageDetails bool, l *logrus.Entry) {
	comment := fmt.Sprintf("@%s, `pj-rehearse`: %s ERROR: \n ```\n%v\n```\n", user, message, err)
	if addContact {
		comment += " If the problem persists, please [contact](https://docs.ci.openshift.org/docs/getting-started/useful-links/#contact) Test Platform."
	}
	if addUsageDetails {
		comment += strings.Join(s.getUsageDetailsLines(), "\n")
	}
	if err := s.ghc.CreateComment(org, repo, number, comment); err != nil {
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
	if rebased, err := repoClient.MergeWithStrategy(baseRef, "rebase"); !rebased {
		return rehearse.RehearsalCandidate{}, fmt.Errorf("couldn't rebase candidate onto master: %w", err)
	}

	return candidate, nil
}

// getJobsTableLines returns a Markdown formatted table of all affected jobs in the form of a []string
// and the total number of affected jobs
func (s *server) getJobsTableLines(presubmits config.Presubmits, periodics config.Periodics, user string) ([]string, int) {
	if len(presubmits) == 0 && len(periodics) == 0 {
		return []string{fmt.Sprintf("%s \n@%s: no rehearsable tests are affected by this change", rehearsalNotifier, user)}, 0
	}

	lines := []string{
		fmt.Sprintf("%s \n@%s: the `pj-rehearse` plugin accommodates running rehearsal tests for the changes in this PR. Expand **'Interacting with pj-rehearse'** for usage details. The following rehearsable tests have been affected by this change:", rehearsalNotifier, user),
		"",
		"Test name | Repo | Type | Reason",
		"--- | --- | --- | ---",
	}

	limitToList := s.rehearsalConfig.MaxLimit
	affectedJobs := getAffectedJobFormattedList(presubmits, periodics)
	for i, job := range affectedJobs {
		if i >= limitToList {
			break
		}
		lines = append(lines, job)
	}

	jobCount := len(affectedJobs)
	if jobCount > limitToList {
		lines = append(lines, "") // For formatting
		lines = append(lines, fmt.Sprintf("A total of %d jobs have been affected by this change. The above listing is non-exhaustive and limited to %d jobs.", jobCount, limitToList))
	}

	return append(lines, ""), jobCount
}

func getAffectedJobFormattedList(presubmits config.Presubmits, periodics config.Periodics) []string {
	var jobs []string
	for repoName, tests := range presubmits {
		for _, presubmit := range tests {
			jobs = append(jobs, fmt.Sprintf("%s | %s | %s | %s", presubmit.Name, repoName, "presubmit", config.GetSourceType(presubmit.Labels).GetDisplayText()))
		}
	}
	for jobName, periodic := range periodics {
		jobs = append(jobs, fmt.Sprintf("%s | N/A | %s | %s", jobName, "periodic", config.GetSourceType(periodic.Labels).GetDisplayText()))
	}
	return jobs
}

func (s *server) getUsageDetailsLines() []string {
	rc := s.rehearsalConfig
	return []string{
		"<details>",
		"<summary>Interacting with pj-rehearse</summary>",
		"",
		fmt.Sprintf("Comment: `%s` to run up to %d rehearsals", rehearseNormal, rc.NormalLimit),
		fmt.Sprintf("Comment: `%s` to opt-out of rehearsals", rehearseSkip),
		fmt.Sprintf("Comment: `%s {test-name}`, with each test separated by a space, to run one or more specific rehearsals", rehearseNormal),
		fmt.Sprintf("Comment: `%s` to run up to %d rehearsals", rehearseMore, rc.MoreLimit),
		fmt.Sprintf("Comment: `%s` to run up to %d rehearsals", rehearseMax, rc.MaxLimit),
		fmt.Sprintf("Comment: `%s` to run up to %d rehearsals, and add the `%s` label on success", rehearseAutoAck, rc.NormalLimit, rehearse.RehearsalsAckLabel),
		fmt.Sprintf("Comment: `%s` to abort all active rehearsals", rehearseAbort),
		"",
		fmt.Sprintf("Once you are satisfied with the results of the rehearsals, comment: `%s` to unblock merge. When the `%s` label is present on your PR, merge will no longer be blocked by rehearsals.", rehearseAck, rehearse.RehearsalsAckLabel),
		fmt.Sprintf("If you would like the `%s` label removed, comment: `%s` to re-block merging.", rehearse.RehearsalsAckLabel, rehearseReject),
		"</details>",
	}
}

func (s *server) getDisabledRehearsalsLines(disabledDueToNetworkAccessToggle []string) []string {
	var lines []string
	if len(disabledDueToNetworkAccessToggle) > 0 {
		lines = append(lines, "The following jobs are not rehearsable due to the `restrict_network_access` field being set to `false` in this PR. You must first merge this PR, and then subsequent changes to the job will be rehearsable: ")
		lines = []string{
			"The following jobs are not rehearsable due to the `restrict_network_access` field being set to `false` in this PR. You must first merge this PR, and then subsequent changes to the job will be rehearsable: ",
			"",
			"Test name |",
			"--- |",
		}
		for _, disabled := range disabledDueToNetworkAccessToggle {
			lines = append(lines, disabled)
		}
	}
	return lines
}

func (s *server) acknowledgeRehearsals(org, repo string, number int, logger *logrus.Entry) {
	if err := s.ghc.AddLabel(org, repo, number, rehearse.RehearsalsAckLabel); err != nil {
		logger.WithError(err).Errorf("failed to add '%s' label", rehearse.RehearsalsAckLabel)
	}
}

func (s *server) dumpAffectedJobsToGCS(pullRequest *github.PullRequest, presubmits config.Presubmits, periodics config.Periodics, jobCount int, logger *logrus.Entry) string {
	logger.WithField("jobCount", jobCount).Debugf("jobCount is above %d. cannot comment all jobs, writing out to file", s.rehearsalConfig.MaxLimit)
	fileContent := []string{"Test Name | Repo | Type | Reason"}
	fileLocation := fmt.Sprintf("%s/%s/%s/%d/%s", pjRehearse, pullRequest.Base.Repo.Owner.Login, pullRequest.Base.Repo.Name, pullRequest.Number, pullRequest.Head.SHA)
	uploadTargets := map[string]gcs.UploadFunc{
		fileLocation: gcs.DataUpload(func() (io.ReadCloser, error) {
			fileContent = append(fileContent, getAffectedJobFormattedList(presubmits, periodics)...)
			return io.NopCloser(strings.NewReader(strings.Join(fileContent, "\n"))), nil
		}),
	}
	if err := gcs.Upload(context.Background(), s.rehearsalConfig.GCSBucket, s.rehearsalConfig.GCSCredentialsFile, "", []string{"*"}, uploadTargets); err != nil {
		logger.WithError(err).Error("couldn't upload affected job data to GCS")
	}
	return fileLocation
}
