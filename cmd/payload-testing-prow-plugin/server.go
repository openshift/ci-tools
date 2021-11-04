package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pluginhelp"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/release/config"
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

type server struct {
	ghc          githubClient
	ctx          context.Context
	kubeClient   ctrlruntimeclient.Client
	namespace    string
	jobResolver  jobResolver
	testResolver testResolver
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
	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	prNumber := ic.Issue.Number

	guid := ic.GUID

	logger := l.WithFields(logrus.Fields{
		github.OrgLogField:  org,
		github.RepoLogField: repo,
		github.PrLogField:   prNumber,
	})

	// only reacts on comments on PRs
	if !ic.Issue.IsPullRequest() {
		logger.Debug("not a pull request")
		return ""
	}

	pr, err := s.ghc.GetPullRequest(org, repo, prNumber)
	if err != nil {
		logger.Debug("could not get pull request")
		return fmt.Sprintf("could not get pull request https://github.com/%s/%s/pull/%d: %v", org, repo, prNumber, err)
	}

	logger.WithField("ic.Comment.Body", ic.Comment.Body).Debug("received a comment")
	specs := specsFromComment(ic.Comment.Body)
	if len(specs) == 0 {
		logger.Debug("found no specs from comments")
		return ""
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
		var jobNames []string
		var jobTuples []api.MetadataWithTest
		jobs, err := s.jobResolver.resolve(spec.ocp, spec.releaseType, spec.jobs)
		if err != nil {
			specLogger.WithError(err).Error("could not resolve jobs")
			return fmt.Sprintf("could not resolve jobs for %s %s %s: %v", spec.ocp, spec.releaseType, spec.jobs, err)
		}
		for _, job := range jobs {
			if job.Test != "" {
				jobNames = append(jobNames, job.Name)
				jobTuples = append(jobTuples, api.MetadataWithTest{
					Metadata: job.Metadata,
					Test:     job.Test,
				})
			} else {
				jobTuple, err := s.testResolver.resolve(job.Name)
				if err != nil {
					// This is expected for non-generated jobs
					specLogger.WithError(err).WithField("job.Name", job.Name).Info("could not resolve tests for job")
					continue
				}
				jobNames = append(jobNames, job.Name)
				jobTuples = append(jobTuples, jobTuple)
			}
		}
		if len(jobTuples) > 0 {
			if err := s.kubeClient.Create(s.ctx, builder.build(jobTuples)); err != nil {
				specLogger.WithError(err).Error("could not create PullRequestPayloadQualificationRun")
				return fmt.Sprintf("could not create PullRequestPayloadQualificationRun: %v", err)
			}
		} else {
			specLogger.Warn("found no resolved tests")
		}
		messages = append(messages, message(spec, jobNames))
	}
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
}

func (b *prpqrBuilder) build(jobTuples []api.MetadataWithTest) *prpqv1.PullRequestPayloadQualificationRun {
	var releaseJobSpecs []prpqv1.ReleaseJobSpec
	for _, jobTuple := range jobTuples {
		releaseJobSpecs = append(releaseJobSpecs, prpqv1.ReleaseJobSpec{
			CIOperatorConfig: jobTuple.Metadata,
			Test:             jobTuple.Test,
		})
	}
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
