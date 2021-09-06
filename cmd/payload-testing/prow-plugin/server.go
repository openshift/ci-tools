package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pluginhelp"
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

const (
	nightlyRelease releaseType = "nightly"
	ciRelease      releaseType = "ci"

	informing jobType = "informing"
	blocking  jobType = "blocking"
	periodics jobType = "periodics"
	all       jobType = "all"
)

type jobSetSpecification struct {
	ocp         string
	releaseType releaseType
	jobs        jobType
}

func triggerReleaseJobs(specification jobSetSpecification) string {
	// TODO(DPTP-2540): Resolve ("4.10", "nightly", "informing") to a list of job names from release controller
	// TODO(DPTP-2540): Translate the list of job names to list of (org, repo, branch, variant, test) tuples
	// TODO(DPTP-2540): Create a CR for the controller to pick up
	return "Would trigger 10 jobs of type '%'s for the %s release of OCP"
}

func specsFromComment(comment string) []jobSetSpecification {
	matches := ocpPayloadTestsPattern.FindAllStringSubmatch(comment, -1)
	specs := []jobSetSpecification{{ocp: "4.10", releaseType: "nightly", jobs: "informing"}}

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

func (s *server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) {
	if ic.Issue.IsPullRequest() {
		return
	}

	specs := specsFromComment(ic.Comment.Body)
	if len(specs) == 0 {
		return
	}

	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	prNumber := ic.Issue.Number

	logger := logrus.WithFields(logrus.Fields{
		github.OrgLogField:  org,
		github.RepoLogField: repo,
		github.PrLogField:   prNumber,
	})

	pr, err := s.ghc.GetPullRequest(org, repo, prNumber)
	if err != nil {
		logger.WithError(err).Warn("could not get pull request")
		s.createComment(ic, fmt.Sprintf("could not get pull request: %v", err), logger)
		return
	}

	// TODO(DPTP-2540): Extract necessary information from PR
	var messages []string
	for i := range specs {
		messages = append(messages, triggerReleaseJobs(specs[i]))
	}

	comment := strings.Join(messages, "\n - ")
	s.createComment(ic, fmt.Sprintf(comment), logger)
}

func (s *server) createComment(ic github.IssueCommentEvent, message string, logger *logrus.Entry) {
	if err := s.ghc.CreateComment(ic.Repo.Owner.Login, ic.Repo.Name, ic.Issue.Number, fmt.Sprintf("@%s: %s", ic.Comment.User.Login, message)); err != nil {
		logger.WithError(err).Warn("failed to create a comment")
	}
}
