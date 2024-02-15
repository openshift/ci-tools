package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/plugins/trigger"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/release/config"
)

const (
	prPayloadTestsUIURL = "https://pr-payload-tests.ci.openshift.org/runs"
)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
}

var (
	ocpPayloadTestsPattern                     = regexp.MustCompile(`(?mi)^/payload\s+(?P<ocp>4\.\d+)\s+(?P<release>\w+)\s+(?P<jobs>\w+)\s*$`)
	ocpPayloadWithPRsTestsPattern              = regexp.MustCompile(`(?mi)^/payload-with-prs\s+(?P<ocp>4\.\d+)\s+(?P<release>\w+)\s+(?P<jobs>\w+)\s+(?P<prs>(?:[-\w./#]+\s*)+)\s*$`)
	ocpPayloadJobTestsPattern                  = regexp.MustCompile(`(?mi)^/payload-job\s+((?:[-\w.]+\s*?)+)\s*$`)
	ocpPayloadJobTestsWithPRsPattern           = regexp.MustCompile(`(?mi)^/payload-job-with-prs\s+(?P<job>[-\w.]+)\s+(?P<prs>(?:[-\w./#]+\s*)+)\s*$`)
	ocpPayloadAggregatedJobTestsPattern        = regexp.MustCompile(`(?mi)^/payload-aggregate\s+(?P<job>[-\w.]+)\s+(?P<aggregate>\d+)\s*$`)
	ocpPayloadAggregatedWithPRsJobTestsPattern = regexp.MustCompile(`(?mi)^/payload-aggregate-with-prs\s+(?P<job>[-\w.]+)\s+(?P<aggregate>\d+)\s+(?P<prs>(?:[-\w./#]+\s*)+)\s*$`)
	ocpPayloadAbortPattern                     = regexp.MustCompile(`(?mi)^/payload-abort$`)
)

func helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `The payload-testing plugin triggers a run of specified release qualification jobs against PR code`,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		// We will likely want to specify which jobs: blocking | informing | analysis | periodics | all
		// We will also likely want to specify which release: ci | nightly
		Usage:       "/payload",
		Description: "The payload-testing plugin triggers a run of specified release qualification jobs against PR code",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/payload 4.10 nightly informing", "/payload 4.8 ci all"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/payload-with-prs",
		Description: "The payload-testing plugin triggers a run of specified release qualification jobs against a payload also including the other mentioned PRs",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/payload 4.10 nightly informing openshift/kubernetes#1234 openshift/installer#999", "/payload 4.8 ci all openshift/kubernetes#1234"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/payload-job",
		Description: "The payload-testing plugin triggers a run of specified job or jobs delimited by spaces",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/payload-job periodic-release-4.14-aws", "/payload-job periodic-release-4.14-aws periodic-ci-openshift-release-master-ci-4.13-e2e-aws-sdn-serial"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/payload-job-with-prs",
		Description: "The payload-testing plugin triggers a run of specified job including the other mentioned PRs in the built payload",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/payload-job-with-prs periodic-release-4.14-aws openshift/kubernetes#1234 openshift/installer#999"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/payload-aggregate",
		Description: "The payload-testing plugin triggers the specified number of runs of the specified job. A special \"aggregator\" job is triggered to aggregate the results",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/payload-aggregate periodic-release-4.14-aws 10"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/payload-aggregate-with-prs",
		Description: "The payload-testing plugin triggers the specified number of runs of the specified job against a payload including the additionally supplied PRs. A special \"aggregator\" job is triggered to aggregate the results",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/payload-aggregate-with-prs periodic-release-4.14-aws 10 openshift/installer#999", "/payload-aggregate-with-prs periodic-release-4.14-aws 5 openshift/kubernetes#123 openshift/installer#999"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/payload-abort",
		Description: "The payload-testing plugin aborts all active payload jobs for the PR",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/payload-abort"},
	})
	return pluginHelp, nil
}

type trustedChecker interface {
	trustedUser(author, org, repo string, num int) (bool, error)
}

type githubTrustedChecker struct {
	githubClient github.Client
}

func (c *githubTrustedChecker) trustedUser(author, org, repo string, _ int) (bool, error) {
	triggerTrustedResponse, err := trigger.TrustedUser(c.githubClient, false, []string{}, "", author, org, repo)
	if err != nil {
		return false, fmt.Errorf("error checking %s for trust: %w", author, err)
	}
	return triggerTrustedResponse.IsTrusted, nil
}

type server struct {
	ghc                githubClient
	ctx                context.Context
	kubeClient         ctrlruntimeclient.Client
	namespace          string
	jobResolver        jobResolver
	testResolver       testResolver
	trustedChecker     trustedChecker
	ciOpConfigResolver ciOpConfigResolver
}

type jobSetSpecification struct {
	ocp           string
	releaseType   api.ReleaseStream
	jobs          config.JobType
	additionalPRs []config.AdditionalPR
}

type jobResolver interface {
	resolve(ocp string, releaseType api.ReleaseStream, jobType config.JobType) ([]config.Job, error)
}

type testResolver interface {
	resolve(job string) (api.MetadataWithTest, error)
}

func specsFromComment(comment string) []jobSetSpecification {
	pattern := ocpPayloadTestsPattern
	matches := pattern.FindAllStringSubmatch(comment, -1)
	if len(matches) == 0 {
		pattern = ocpPayloadWithPRsTestsPattern
		matches = pattern.FindAllStringSubmatch(comment, -1)
		if len(matches) == 0 {
			return nil
		}
	}

	var specs []jobSetSpecification
	ocpIdx := pattern.SubexpIndex("ocp")
	releaseIdx := pattern.SubexpIndex("release")
	jobsIdx := pattern.SubexpIndex("jobs")
	prsIdx := pattern.SubexpIndex("prs")

	for i := range matches {
		var additionalPRs []config.AdditionalPR
		if prsIdx >= 0 {
			for _, pr := range strings.Fields(matches[i][prsIdx]) {
				additionalPRs = append(additionalPRs, config.AdditionalPR(pr))
			}
		}
		specs = append(specs, jobSetSpecification{
			ocp:           matches[i][ocpIdx],
			releaseType:   api.ReleaseStream(matches[i][releaseIdx]),
			jobs:          config.JobType(matches[i][jobsIdx]),
			additionalPRs: additionalPRs,
		})
	}
	return specs
}

func jobsFromComment(comment string) []config.Job {
	var ret []config.Job
	for _, match := range ocpPayloadJobTestsPattern.FindAllStringSubmatch(comment, -1) {
		if len(match) < 2 {
			// This should never happen
			logrus.WithField("match", match).WithField("comment", comment).Error("failed to parse the comment because len(match)<2")
			continue
		}
		for _, jobName := range strings.Fields(match[1]) {
			ret = append(ret, config.Job{
				Name: jobName,
			})
		}
	}

	jobsForPattern := func(pattern *regexp.Regexp, comment string) []config.Job {
		var jobs []config.Job
		for _, match := range pattern.FindAllStringSubmatch(comment, -1) {
			jobIndex := pattern.SubexpIndex("job")
			aggregateIndex := pattern.SubexpIndex("aggregate")
			var aggregatedCount int
			if aggregateIndex >= 0 {
				var err error
				aggregatedCount, err = strconv.Atoi(match[aggregateIndex])
				if err != nil {
					// This should never happen
					logrus.WithField("match", match).WithField("comment", comment).WithError(err).Error("failed to parse the aggregated job")
					continue
				}
			}
			prsIndex := pattern.SubexpIndex("prs")
			var additionalPRs []config.AdditionalPR
			if prsIndex >= 0 {
				rawPRs := strings.Fields(match[prsIndex])
				for _, pr := range rawPRs {
					additionalPRs = append(additionalPRs, config.AdditionalPR(pr))
				}
			}
			jobs = append(jobs, config.Job{
				Name:            match[jobIndex],
				AggregatedCount: aggregatedCount,
				WithPRs:         additionalPRs,
			})
		}
		return jobs
	}

	ret = append(ret, jobsForPattern(ocpPayloadAggregatedJobTestsPattern, comment)...)
	ret = append(ret, jobsForPattern(ocpPayloadJobTestsWithPRsPattern, comment)...)
	ret = append(ret, jobsForPattern(ocpPayloadAggregatedWithPRsJobTestsPattern, comment)...)
	return ret
}

const (
	pluginName = "payload-testing"
)

func (s *server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) {
	if comment, additionalPRs := s.handle(l, ic); comment != "" {
		org := ic.Repo.Owner.Login
		repo := ic.Repo.Name
		number := ic.Issue.Number
		user := ic.Comment.User.Login
		s.createComment(org, repo, number, comment, user, l)
		originalPRRef := fmt.Sprintf("%s/%s#%d", org, repo, number)
		for _, pr := range additionalPRs {
			o, r, n, err := pr.GetOrgRepoAndNumber()
			if err != nil {
				l.WithError(err).Errorf("unable to determine PR from string: %s", pr)
				continue
			}
			additionalComment := fmt.Sprintf("This PR was included in a payload test run from %s\n%s", originalPRRef, comment)
			s.createComment(o, r, n, additionalComment, user, l)
		}
	}
}

// handle determines what commands, if any, are relevant to this plugin and executes them.
// it returns a message about what was done including relevant links, and a slice of AdditionalPRs that were included
func (s *server) handle(l *logrus.Entry, ic github.IssueCommentEvent) (string, []config.AdditionalPR) {
	start := time.Now()
	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	prNumber := ic.Issue.Number

	guid := ic.GUID

	logger := l.WithFields(logrus.Fields{
		github.OrgLogField:  org,
		github.RepoLogField: repo,
		github.PrLogField:   prNumber,
		github.EventGUID:    guid,
	})

	// only reacts on comments on PRs
	if !ic.Issue.IsPullRequest() {
		logger.Trace("not a pull request")
		return "", nil
	}

	logger.WithField("ic.Comment.Body", ic.Comment.Body).Trace("received a comment")

	specs := specsFromComment(ic.Comment.Body)
	jobsFromComment := jobsFromComment(ic.Comment.Body)
	if len(specs) == 0 {
		logger.Trace("found no specs from comment")
	}

	if len(jobsFromComment) == 0 {
		logger.Trace("found no job names from comment")
	} else {
		logger.WithField("jobsFromComment", jobsFromComment).Trace("found job names from comment")
		specs = append(specs, jobSetSpecification{})
	}

	abortRequested := ocpPayloadAbortPattern.MatchString(strings.TrimSpace(ic.Comment.Body))
	if len(specs) == 0 && !abortRequested {
		return "", nil
	}

	startTrustedUser := time.Now()
	trusted, err := s.trustedChecker.trustedUser(ic.Comment.User.Login, org, repo, prNumber)
	logger.WithField("duration", time.Since(startTrustedUser)).Debug("trustedUser completed")
	if err != nil {
		logger.WithError(err).WithField("user", ic.Comment.User.Login).Error("could not check if the user is trusted")
		return formatError(fmt.Errorf("could not check if the user %s is trusted for pull request %s/%s#%d: %w", ic.Comment.User.Login, org, repo, prNumber, err)), nil
	}
	if !trusted {
		logger.WithField("user", ic.Comment.User.Login).Error("the user is not trusted")
		return fmt.Sprintf("user %s is not trusted for pull request %s/%s#%d", ic.Comment.User.Login, org, repo, prNumber), nil
	}

	if abortRequested {
		return s.abortAll(logger, ic), nil
	}

	startGetPullRequest := time.Now()
	pr, err := s.ghc.GetPullRequest(org, repo, prNumber)
	logger.WithField("duration", time.Since(startGetPullRequest)).Debug("GetPullRequest completed")
	if err != nil {
		logger.WithError(err).Error("could not get pull request")
		return formatError(fmt.Errorf("could not get pull request https://github.com/%s/%s/pull/%d: %w", org, repo, prNumber, err)), nil
	}

	ciOpConfig, err := s.ciOpConfigResolver.Config(&api.Metadata{Org: org, Repo: repo, Branch: pr.Base.Ref})
	if err != nil {
		logger.WithError(err).Error("could not resolve ci-operator's config")
		return formatError(fmt.Errorf("could not resolve ci-operator's config for %s/%s/%s: %w", org, repo, pr.Base.Ref, err)), nil
	}
	if !api.PromotesOfficialImages(ciOpConfig, api.WithOKD) {
		logger.Info("the repo does not contribute to the OpenShift official images")
		return fmt.Sprintf("the repo %s/%s does not contribute to the OpenShift official images", org, repo), nil
	}

	var messages []string
	builder := &prpqrBuilder{
		namespace: s.namespace,
		org:       org,
		repo:      repo,
		prNumber:  prNumber,
		guid:      guid,
		counter:   0,
		pr:        pr,
	}

	includedAdditionalPRs := sets.New[config.AdditionalPR]()
	for _, spec := range specs {
		specLogger := logger.WithFields(logrus.Fields{
			"ocp":           spec.ocp,
			"releaseType":   spec.releaseType,
			"jobs":          spec.jobs,
			"additionalPRs": spec.additionalPRs,
		})
		builder.spec = spec
		var jobNames []string
		var releaseJobSpecs []prpqv1.ReleaseJobSpec

		var jobs []config.Job
		if spec.ocp == "" {
			jobs = jobsFromComment
		} else {
			specLogger.Debug("resolving jobs ...")
			startResolveJobs := time.Now()
			resolvedJobs, err := s.jobResolver.resolve(spec.ocp, spec.releaseType, spec.jobs)
			specLogger.WithField("duration", time.Since(startResolveJobs)).WithField("len(jobs)", len(jobs)).
				Debug("resolving jobs completed")
			if err != nil {
				specLogger.WithError(err).Error("could not resolve jobs")
				return formatError(fmt.Errorf("could not resolve jobs for %s %s %s: %w", spec.ocp, spec.releaseType, spec.jobs, err)), nil
			}
			for _, job := range resolvedJobs {
				job.WithPRs = spec.additionalPRs
				jobs = append(jobs, job)
			}
		}

		specLogger.Debug("resolving tests ...")
		startResolveTests := time.Now()
		for _, job := range jobs {
			for _, prRef := range job.WithPRs {
				includedAdditionalPRs.Insert(prRef)
			}
			if job.Test != "" {
				jobNames = append(jobNames, job.Name)
				releaseJobSpecs = append(releaseJobSpecs, prpqv1.ReleaseJobSpec{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     job.Metadata.Org,
						Repo:    job.Metadata.Repo,
						Branch:  job.Metadata.Branch,
						Variant: job.Metadata.Variant,
					},
					Test:            job.Test,
					AggregatedCount: job.AggregatedCount,
				})
			} else {
				jobTuple, err := s.testResolver.resolve(job.Name)
				if err != nil {
					// This is expected for non-generated jobs
					specLogger.WithError(err).WithField("job.Name", job.Name).Info("could not resolve tests for job")
					continue
				}
				jobNames = append(jobNames, job.Name)
				releaseJobSpecs = append(releaseJobSpecs, prpqv1.ReleaseJobSpec{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     jobTuple.Metadata.Org,
						Repo:    jobTuple.Metadata.Repo,
						Branch:  jobTuple.Metadata.Branch,
						Variant: jobTuple.Metadata.Variant,
					},
					Test:            jobTuple.Test,
					AggregatedCount: job.AggregatedCount,
				})
			}
		}

		var additionalPRs []prpqv1.PullRequestUnderTest
		for prRef := range includedAdditionalPRs {
			prOrg, prRepo, number, err := prRef.GetOrgRepoAndNumber()
			if err != nil {
				specLogger.WithError(err).Errorf("unable to get additional pr info from string: %s", prRef)
				return formatError(fmt.Errorf("unable to get additional pr info from string: %s: %w", prRef, err)), nil
			}
			pullRequest, err := s.ghc.GetPullRequest(prOrg, prRepo, number)
			if err != nil {
				specLogger.WithError(err).Errorf("unable to get pr from github for: %s", prRef)
				return formatError(fmt.Errorf("unable to get pr from github for: %s: %w", prRef, err)), nil
			}
			additionalPRs = append(additionalPRs, prpqv1.PullRequestUnderTest{
				Org:     prOrg,
				Repo:    prRepo,
				BaseRef: pullRequest.Base.Ref,
				BaseSHA: pullRequest.Base.SHA,
				PullRequest: &prpqv1.PullRequest{
					Number: number,
					Author: pullRequest.User.Login,
					SHA:    pullRequest.Head.SHA,
					Title:  pullRequest.Title,
				},
			})
		}

		specLogger.WithField("duration", time.Since(startResolveTests)).WithField("len(jobNames)", len(jobNames)).
			Debug("resolving tests completed")
		if len(releaseJobSpecs) > 0 {
			specLogger.Debug("creating PullRequestPayloadQualificationRuns ...")
			startCreateRuns := time.Now()
			run := builder.build(releaseJobSpecs, additionalPRs)
			if err := s.kubeClient.Create(s.ctx, run); err != nil {
				specLogger.WithError(err).Error("could not create PullRequestPayloadQualificationRun")
				return formatError(fmt.Errorf("could not create PullRequestPayloadQualificationRun: %w", err)), nil
			}
			messages = append(messages, message(spec, jobNames))
			messages = append(messages, fmt.Sprintf("See details on %s/%s/%s\n", prPayloadTestsUIURL, builder.namespace, run.Name))

			specLogger.WithField("duration", time.Since(startCreateRuns)).WithField("run.Name", run.Name).
				WithField("run.Namespace", run.Namespace).Debug("creating PullRequestPayloadQualificationRuns completed")
		} else {
			specLogger.Warn("found no resolved tests")
			messages = append(messages, message(spec, jobNames))
		}
	}
	logger.WithField("duration", time.Since(start)).Debug("handle completed")
	return strings.Join(messages, "\n"), includedAdditionalPRs.UnsortedList()
}

func (s *server) abortAll(logger *logrus.Entry, ic github.IssueCommentEvent) string {
	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	prNumber := ic.Issue.Number

	jobs, err := s.getPayloadJobsForPR(org, repo, prNumber, logger)
	if err != nil {
		return formatError(err)
	}
	if len(jobs) == 0 {
		return fmt.Sprintf("no active payload jobs found to abort for pull request %s/%s#%d", org, repo, prNumber)
	}

	var erroredJobs []string
	for _, jobName := range jobs {
		jobLogger := logger.WithField("jobName", jobName)
		job := &prowapi.ProwJob{}
		if err := s.kubeClient.Get(s.ctx, ctrlruntimeclient.ObjectKey{Name: jobName, Namespace: s.namespace}, job); err != nil {
			jobLogger.WithError(err).Error("failed to get prowjob")
			erroredJobs = append(erroredJobs, jobName)
			continue
		}
		// Do not abort jobs that already completed
		if job.Complete() {
			continue
		}
		jobLogger.Debugf("aborting prowjob")
		job.Status.State = prowapi.AbortedState
		// We use Update and not Patch here, because we are not the authority of the .Status.State field
		// and must not overwrite changes made to it in the interim by the responsible agent.
		// The accepted trade-off for now is that this leads to failure if unrelated fields where changed
		// by another different actor.
		if err = s.kubeClient.Update(s.ctx, job); err != nil {
			jobLogger.WithError(err).Errorf("failed to abort prowjob")
			erroredJobs = append(erroredJobs, jobName)
		} else {
			jobLogger.Debugf("aborted prowjob")
		}
	}

	if len(erroredJobs) > 0 {
		return fmt.Sprintf("Failed to abort %d payload jobs out of %d. Failed jobs: %s", len(erroredJobs), len(jobs), strings.Join(erroredJobs, ", "))
	}

	return fmt.Sprintf("aborted active payload jobs for pull request %s/%s#%d", org, repo, prNumber)
}

func (s *server) getPayloadJobsForPR(org, repo string, prNumber int, logger *logrus.Entry) ([]string, error) {
	var l prpqv1.PullRequestPayloadQualificationRunList
	labelSelector, err := labelSelectorForPayloadPRPQRs(org, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("could not create label selector for prpqrs generated for pull request %s/%s#%d: %w", org, repo, prNumber, err)
	}
	opt := ctrlruntimeclient.ListOptions{Namespace: s.namespace, LabelSelector: labelSelector}
	if err := s.kubeClient.List(s.ctx, &l, &opt); err != nil {
		logger.WithError(err).Error("failed to list runs")
		return nil, fmt.Errorf("failed to gather payload job runs for pull request %s/%s#%d in order to abort", org, repo, prNumber)
	}

	var jobs []string
	for _, item := range l.Items {
		for _, job := range item.Status.Jobs {
			state := job.Status.State
			if state == prowapi.TriggeredState || state == prowapi.PendingState {
				jobs = append(jobs, job.ProwJob)
			}
		}
	}

	return jobs, nil
}

func labelSelectorForPayloadPRPQRs(org, repo string, prNumber int) (labels.Selector, error) {
	orgRequirement, err := labels.NewRequirement(kube.OrgLabel, selection.Equals, []string{org})
	if err != nil {
		return nil, err
	}
	repoRequirement, err := labels.NewRequirement(kube.RepoLabel, selection.Equals, []string{repo})
	if err != nil {
		return nil, err
	}
	pullRequirement, err := labels.NewRequirement(kube.PullLabel, selection.Equals, []string{strconv.Itoa(prNumber)})
	if err != nil {
		return nil, err
	}

	return labels.NewSelector().Add(*orgRequirement, *repoRequirement, *pullRequirement), nil
}

type prpqrBuilder struct {
	namespace string
	org       string
	repo      string
	prNumber  int
	guid      string
	counter   int
	pr        *github.PullRequest
	spec      jobSetSpecification
}

func (b *prpqrBuilder) build(releaseJobSpecs []prpqv1.ReleaseJobSpec, additionalPRs []prpqv1.PullRequestUnderTest) *prpqv1.PullRequestPayloadQualificationRun {

	run := &prpqv1.PullRequestPayloadQualificationRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", b.guid, b.counter),
			Namespace: b.namespace,
			Labels: map[string]string{
				api.DPTPRequesterLabel: pluginName,
				kube.OrgLabel:          b.org,
				kube.RepoLabel:         b.repo,
				kube.BaseRefLabel:      b.pr.Base.Ref,
				kube.PullLabel:         strconv.Itoa(b.prNumber),
				"event-GUID":           b.guid,
			},
		},
		Spec: prpqv1.PullRequestPayloadTestSpec{
			Jobs: prpqv1.PullRequestPayloadJobSpec{
				Jobs: releaseJobSpecs,
				ReleaseControllerConfig: prpqv1.ReleaseControllerConfig{
					OCP:       b.spec.ocp,
					Release:   string(b.spec.releaseType),
					Specifier: string(b.spec.jobs),
				},
			},
			PullRequests: append(additionalPRs, prpqv1.PullRequestUnderTest{
				Org:     b.org,
				Repo:    b.repo,
				BaseRef: b.pr.Base.Ref,
				BaseSHA: b.pr.Base.SHA,
				PullRequest: &prpqv1.PullRequest{
					Number: b.prNumber,
					Author: b.pr.User.Login,
					SHA:    b.pr.Head.SHA,
					Title:  b.pr.Title,
				},
			}),
		},
	}
	b.counter++
	return run
}

func message(spec jobSetSpecification, tests []string) string {
	var b strings.Builder
	if spec.ocp == "" {
		b.WriteString(fmt.Sprintf("trigger %d job(s) for the /payload-(with-prs|job|aggregate|job-with-prs|aggregate-with-prs) command\n", len(tests)))
	} else {
		b.WriteString(fmt.Sprintf("trigger %d job(s) of type %s for the %s release of OCP %s\n", len(tests), spec.jobs, spec.releaseType, spec.ocp))
	}
	for _, test := range tests {
		b.WriteString(fmt.Sprintf("- %s\n", test))
	}
	return b.String()
}

func (s *server) createComment(org, repo string, number int, message, user string, logger *logrus.Entry) {
	if err := s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s: %s", user, message)); err != nil {
		logger.WithError(err).Error("failed to create a comment")
	}
}

func formatError(err error) string {
	knownErrors := map[string]string{
		"could not create PullRequestPayloadQualificationRun: context canceled": "The pod running the tool gets restarted. Please try again later.",
	}
	var applicable []string
	for key, value := range knownErrors {
		if strings.Contains(err.Error(), key) {
			applicable = append(applicable, value)
		}
	}
	digest := "No known errors were detected, please see the full error message for details."
	if len(applicable) > 0 {
		digest = "We were able to detect the following conditions from the error:\n\n"
		for _, item := range applicable {
			digest = fmt.Sprintf("%s- %s\n", digest, item)
		}
	}
	return fmt.Sprintf(`An error was encountered. %s

<details><summary>Full error message.</summary>

<code>
%v
</code>

</details>

Please contact an administrator to resolve this issue.`,
		digest, err)
}

type ciOpConfigResolver interface {
	Config(*api.Metadata) (*api.ReleaseBuildConfiguration, error)
}
