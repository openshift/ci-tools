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
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/plugins/trigger"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/promotion"
	"github.com/openshift/ci-tools/pkg/release/config"
)

const (
	prPayloadTestsUIURL = "https://pr-payload-tests.ci.openshift.org/runs"
)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
}

var ocpPayloadTestsPattern = regexp.MustCompile(`(?mi)^/payload\s+(?P<ocp>4\.\d+)\s+(?P<release>\w+)\s+(?P<jobs>\w+)\s*$`)

func helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	// TODO(DPTP-2540): Better descriptions, better help
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
	return pluginHelp, nil
}

type trustedChecker interface {
	trustedUser(author, org, repo string, num int) (bool, error)
}

type githubTrustedChecker struct {
	githubClient github.Client
}

func (c *githubTrustedChecker) trustedUser(author, org, repo string, _ int) (bool, error) {
	triggerTrustedResponse, err := trigger.TrustedUser(c.githubClient, false, "", author, org, repo)
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
	ocp         string
	releaseType api.ReleaseStream
	jobs        config.JobType
}

type jobResolver interface {
	resolve(ocp string, releaseType api.ReleaseStream, jobType config.JobType) ([]config.Job, error)
}

type testResolver interface {
	resolve(job string) (api.MetadataWithTest, error)
}

func specsFromComment(comment string) []jobSetSpecification {
	matches := ocpPayloadTestsPattern.FindAllStringSubmatch(comment, -1)
	if len(matches) == 0 {
		return nil
	}
	var specs []jobSetSpecification
	ocpIdx := ocpPayloadTestsPattern.SubexpIndex("ocp")
	releaseIdx := ocpPayloadTestsPattern.SubexpIndex("release")
	jobsIdx := ocpPayloadTestsPattern.SubexpIndex("jobs")

	for i := range matches {
		specs = append(specs, jobSetSpecification{
			ocp:         matches[i][ocpIdx],
			releaseType: api.ReleaseStream(matches[i][releaseIdx]),
			jobs:        config.JobType(matches[i][jobsIdx]),
		})
	}
	return specs
}

const (
	pluginName = "payload-testing"
)

func (s *server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) {
	if comment := s.handle(l, ic); comment != "" {
		s.createComment(ic, comment, l)
	}
}

func (s *server) handle(l *logrus.Entry, ic github.IssueCommentEvent) string {
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
		return ""
	}

	logger.WithField("ic.Comment.Body", ic.Comment.Body).Trace("received a comment")
	specs := specsFromComment(ic.Comment.Body)
	if len(specs) == 0 {
		logger.Trace("found no specs from comments")
		return ""
	}

	startTrustedUser := time.Now()
	trusted, err := s.trustedChecker.trustedUser(ic.Comment.User.Login, org, repo, prNumber)
	logger.WithField("duration", time.Since(startTrustedUser)).Debug("trustedUser completed")
	if err != nil {
		logger.WithError(err).WithField("user", ic.Comment.User.Login).Error("could not check if the user is trusted")
		return formatError(fmt.Errorf("could not check if the user %s is trusted for pull request %s/%s#%d: %w", ic.Comment.User.Login, org, repo, prNumber, err))
	}
	if !trusted {
		logger.WithField("user", ic.Comment.User.Login).Error("the user is not trusted")
		return fmt.Sprintf("user %s is not trusted for pull request %s/%s#%d", ic.Comment.User.Login, org, repo, prNumber)
	}

	startGetPullRequest := time.Now()
	pr, err := s.ghc.GetPullRequest(org, repo, prNumber)
	logger.WithField("duration", time.Since(startGetPullRequest)).Debug("GetPullRequest completed")
	if err != nil {
		logger.WithError(err).Error("could not get pull request")
		return formatError(fmt.Errorf("could not get pull request https://github.com/%s/%s/pull/%d: %w", org, repo, prNumber, err))
	}

	ciOpConfig, err := s.ciOpConfigResolver.Config(&api.Metadata{Org: org, Repo: repo, Branch: pr.Base.Ref})
	if err != nil {
		logger.WithError(err).Error("could not resolve ci-operator's config")
		return formatError(fmt.Errorf("could not resolve ci-operator's config for %s/%s/%s: %w", org, repo, pr.Base.Ref, err))
	}
	if !promotion.PromotesOfficialImages(ciOpConfig) {
		logger.Error("the repo does not contribute to the OpenShift official images")
		return fmt.Sprintf("the repo %s/%s does not contribute to the OpenShift official images", org, repo)
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
	for _, spec := range specs {
		specLogger := logger.WithFields(logrus.Fields{
			"ocp":         spec.ocp,
			"releaseType": spec.releaseType,
			"jobs":        spec.jobs,
		})
		builder.spec = spec
		var jobNames []string
		specLogger.Debug("resolving jobs ...")
		startResolveJobs := time.Now()
		jobs, err := s.jobResolver.resolve(spec.ocp, spec.releaseType, spec.jobs)
		specLogger.WithField("duration", time.Since(startResolveJobs)).WithField("len(jobs)", len(jobs)).
			Debug("resolving jobs completed")
		if err != nil {
			specLogger.WithError(err).Error("could not resolve jobs")
			return formatError(fmt.Errorf("could not resolve jobs for %s %s %s: %w", spec.ocp, spec.releaseType, spec.jobs, err))
		}
		specLogger.Debug("resolving tests ...")
		startResolveTests := time.Now()

		var releaseJobSpecs []prpqv1.ReleaseJobSpec
		for _, job := range jobs {
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

		specLogger.WithField("duration", time.Since(startResolveTests)).WithField("len(jobNames)", len(jobNames)).
			Debug("resolving tests completed")
		if len(releaseJobSpecs) > 0 {
			specLogger.Debug("creating PullRequestPayloadQualificationRuns ...")
			startCreateRuns := time.Now()
			run := builder.build(releaseJobSpecs)
			if err := s.kubeClient.Create(s.ctx, run); err != nil {
				specLogger.WithError(err).Error("could not create PullRequestPayloadQualificationRun")
				return formatError(fmt.Errorf("could not create PullRequestPayloadQualificationRun: %w", err))
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
	return strings.Join(messages, "\n")
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

func (b *prpqrBuilder) build(releaseJobSpecs []prpqv1.ReleaseJobSpec) *prpqv1.PullRequestPayloadQualificationRun {
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
			PullRequest: prpqv1.PullRequestUnderTest{
				Org:     b.org,
				Repo:    b.repo,
				BaseRef: b.pr.Base.Ref,
				BaseSHA: b.pr.Base.SHA,
				PullRequest: prpqv1.PullRequest{
					Number: b.prNumber,
					Author: b.pr.User.Login,
					SHA:    b.pr.Head.SHA,
					Title:  b.pr.Title,
				},
			},
		},
	}
	b.counter++
	return run
}

func message(spec jobSetSpecification, tests []string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("trigger %d jobs of type %s for the %s release of OCP %s\n", len(tests), spec.jobs, spec.releaseType, spec.ocp))
	for _, test := range tests {
		b.WriteString(fmt.Sprintf("- %s\n", test))
	}
	return b.String()
}

func (s *server) createComment(ic github.IssueCommentEvent, message string, logger *logrus.Entry) {
	if err := s.ghc.CreateComment(ic.Repo.Owner.Login, ic.Repo.Name, ic.Issue.Number, fmt.Sprintf("@%s: %s", ic.Comment.User.Login, message)); err != nil {
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
