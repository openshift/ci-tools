package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pluginhelp"

	"github.com/openshift/ci-tools/pkg/api"
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
	ghc githubClient
}

type releaseType string
type jobType string

type jobSetSpecification struct {
	ocp         string
	releaseType releaseType
	jobs        jobType
}

func triggerReleaseJobs(specification jobSetSpecification, meta api.Metadata, tests []string) error {
	// TODO(DPTP-2540): Translate the list of job names to list of (org, repo, branch, variant, test) tuples
	// TODO(DPTP-2540): Create a CR for the controller to pick up
	return nil
}

func resolve(ocp string, releaseType releaseType, jobType jobType) []string {
	// TODO(DPTP-2540): Resolve ("4.10", "nightly", "informing") to a list of job names from release controller
	return []string{fmt.Sprintf("dummy-ocp-%s-%s-%s-job1", ocp, releaseType, jobType), fmt.Sprintf("dummy-ocp-%s-%s-%s-job2", ocp, releaseType, jobType)}
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
			releaseType: releaseType(matches[i][releaseIdx]),
			jobs:        jobType(matches[i][jobsIdx]),
		})
	}
	return specs
}

const (
	pluginName = "payload-testing"
)

func (s *server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) {
	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	prNumber := ic.Issue.Number

	logger := l.WithFields(logrus.Fields{
		github.OrgLogField:  org,
		github.RepoLogField: repo,
		github.PrLogField:   prNumber,
		"plugin":            pluginName,
	})

	// only reacts on comments on PRs
	if !ic.Issue.IsPullRequest() {
		logger.Debug("not a pull request")
		return
	}

	logger.WithField("ic.Comment.Body", ic.Comment.Body).Debug("received a comment")
	specs := specsFromComment(ic.Comment.Body)
	if len(specs) == 0 {
		logger.Debug("found no specs from comments")
		return
	}

	pr, err := s.ghc.GetPullRequest(org, repo, prNumber)
	if err != nil {
		logger.WithError(err).Warn("could not get pull request")
		s.createComment(ic, fmt.Sprintf("could not get pull request: %v", err), logger)
		return
	}

	meta := api.Metadata{
		Org:    ic.Repo.Owner.Login,
		Repo:   ic.Repo.Name,
		Branch: pr.Base.Ref,
	}

	// TODO(DPTP-2540): Extract necessary information from PR
	var messages []string
	for _, spec := range specs {
		tests := resolve(spec.ocp, spec.releaseType, spec.jobs)
		if err := triggerReleaseJobs(spec, meta, tests); err != nil {
			logger.WithError(err).Warn("could not trigger release jobs")
			s.createComment(ic, fmt.Sprintf("could not trigger release jobs: %v", err), logger)
			continue
		}
		messages = append(messages, message(spec, tests))
	}

	comment := strings.Join(messages, "\n")
	s.createComment(ic, comment, logger)
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
		logger.WithError(err).Warn("failed to create a comment")
	}
}
