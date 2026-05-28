package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins/trigger"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
)

const (
	onboardCommand = "/onboard"
	newTestPrefix  = "/new-test"
	ciOperatorDir  = ".ci-operator"
)

var pluginGenerator = jc.Generator(pluginName)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetDirectory(org, repo, dirpath, commit string) ([]github.DirectoryContent, error)
	GetFile(org, repo, filepath, commit string) ([]byte, error)
}

type trustedChecker interface {
	trustedUser(author, org, repo string, num int) (bool, error)
}

type githubTrustedChecker struct {
	githubClient github.Client
}

func (c *githubTrustedChecker) trustedUser(author, org, repo string, _ int) (bool, error) {
	resp, err := trigger.TrustedUser(c.githubClient, false, []string{}, "", author, org, repo)
	if err != nil {
		return false, fmt.Errorf("error checking %s for trust: %w", author, err)
	}
	return resp.IsTrusted, nil
}

type server struct {
	ghc              githubClient
	trustedChecker   trustedChecker
	pjclient         ctrlruntimeclient.Client
	namespace        string
	jobConfigDir     string
	releaseRepoDir   string
	prowgenImage     string
	checkconfigImage string
}

func helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The in-repo-config plugin helps repos onboard to in-repo CI configuration and manage ephemeral test jobs from PRs.",
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/onboard",
		Description: "Bootstrap in-repo CI config by generating a config-checker presubmit and a prowgen postsubmit on EFS.",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/onboard"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/new-test [testname]",
		Description: "Generate ephemeral ProwJob definitions from the PR's .ci-operator/ configs so new tests are immediately available.",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/new-test e2e", "/new-test unit"},
	})
	return pluginHelp, nil
}

func (s *server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) {
	if ic.Action != github.IssueCommentActionCreated {
		return
	}
	if !ic.Issue.IsPullRequest() {
		return
	}

	body := strings.TrimSpace(ic.Comment.Body)
	switch {
	case body == onboardCommand:
		s.handleOnboard(l, ic)
	case strings.HasPrefix(body, newTestPrefix):
		s.handleNewTest(l, ic)
	}
}

func (s *server) handleOnboard(l *logrus.Entry, ic github.IssueCommentEvent) {
	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	number := ic.Issue.Number
	user := ic.Comment.User.Login

	logger := l.WithFields(logrus.Fields{
		"org": org, "repo": repo, "pr": number, "command": "onboard",
	})

	trusted, err := s.trustedChecker.trustedUser(user, org, repo, number)
	if err != nil {
		logger.WithError(err).Error("could not check if user is trusted")
		s.commentError(org, repo, number, user, "onboard", err, logger)
		return
	}
	if !trusted {
		logger.WithField("user", user).Warn("untrusted user")
		s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s: you are not trusted to use `/onboard`.", user))
		return
	}

	efsPath := filepath.Join(s.jobConfigDir, org, repo)
	releasePath := filepath.Join(s.releaseRepoDir, "ci-operator/config", org, repo)

	efsExists := dirExists(efsPath)
	releaseExists := dirExists(releasePath)

	if releaseExists {
		msg := fmt.Sprintf("@%s: jobs for `%s/%s` already exist in the centralized openshift/release repository at `ci-operator/config/%s/%s`. "+
			"Please remove them from openshift/release before onboarding to in-repo config.", user, org, repo, org, repo)
		s.ghc.CreateComment(org, repo, number, msg)
		return
	}
	if efsExists {
		msg := fmt.Sprintf("@%s: jobs for `%s/%s` already exist on EFS. "+
			"If you need to re-onboard, please remove the existing job configs first.", user, org, repo)
		s.ghc.CreateComment(org, repo, number, msg)
		return
	}

	pr, err := s.ghc.GetPullRequest(org, repo, number)
	if err != nil {
		logger.WithError(err).Error("could not get pull request")
		s.commentError(org, repo, number, user, "onboard", err, logger)
		return
	}
	branch := pr.Base.Ref

	jobConfig := generateBootstrapJobs(org, repo, branch, s.prowgenImage, s.checkconfigImage)
	if err := jc.WriteToDir(s.jobConfigDir, org, repo, jobConfig, pluginGenerator, nil); err != nil {
		logger.WithError(err).Error("could not write bootstrap jobs to EFS")
		s.commentError(org, repo, number, user, "onboard", err, logger)
		return
	}

	msg := fmt.Sprintf("@%s: successfully onboarded `%s/%s` (branch `%s`) to in-repo CI config.\n\n"+
		"Bootstrap jobs created:\n"+
		"- `pull-ci-%s-%s-%s-ci-operator-config-check` (presubmit): validates `.ci-operator/` configs\n"+
		"- `branch-ci-%s-%s-%s-prowgen` (postsubmit): generates permanent job definitions on merge\n\n"+
		"Jobs will be available within ~1 second via Prow hot-reload.",
		user, org, repo, branch,
		org, repo, branch,
		org, repo, branch)
	s.ghc.CreateComment(org, repo, number, msg)
	logger.Info("onboarding complete")
}

func (s *server) handleNewTest(l *logrus.Entry, ic github.IssueCommentEvent) {
	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	number := ic.Issue.Number
	user := ic.Comment.User.Login

	logger := l.WithFields(logrus.Fields{
		"org": org, "repo": repo, "pr": number, "command": "new-test",
	})

	trusted, err := s.trustedChecker.trustedUser(user, org, repo, number)
	if err != nil {
		logger.WithError(err).Error("could not check if user is trusted")
		s.commentError(org, repo, number, user, "new-test", err, logger)
		return
	}
	if !trusted {
		logger.WithField("user", user).Warn("untrusted user")
		s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s: you are not trusted to use `/new-test`.", user))
		return
	}

	pr, err := s.ghc.GetPullRequest(org, repo, number)
	if err != nil {
		logger.WithError(err).Error("could not get pull request")
		s.commentError(org, repo, number, user, "new-test", err, logger)
		return
	}
	sha := pr.Head.SHA
	branch := pr.Base.Ref

	configs, err := s.fetchConfigs(org, repo, sha, logger)
	if err != nil {
		logger.WithError(err).Error("could not fetch .ci-operator/ configs")
		s.commentError(org, repo, number, user, "new-test", err, logger)
		return
	}
	if len(configs) == 0 {
		s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s: no `.ci-operator/` configs found in this PR at commit %s.", user, shortSHA(sha)))
		return
	}

	orgrepo := fmt.Sprintf("%s/%s", org, repo)
	allJobs := &prowconfig.JobConfig{
		PresubmitsStatic:  map[string][]prowconfig.Presubmit{},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
	}

	for filename, configSpec := range configs {
		info := metadataFromFilename(filename, org, repo, branch)
		configSpec.UnresolvedConfigPath = cioperatorapi.CIOperatorInrepoConfigFileName
		generated, err := prowgen.GenerateJobs(configSpec, info)
		if err != nil {
			logger.WithError(err).WithField("file", filename).Error("prowgen failed")
			s.commentError(org, repo, number, user, "new-test", fmt.Errorf("prowgen failed for %s: %w", filename, err), logger)
			return
		}
		jc.Append(allJobs, generated)
	}

	existingNames := map[string]bool{}
	permanentPath := filepath.Join(s.jobConfigDir, org, repo)
	if dirExists(permanentPath) {
		existing, err := jc.ReadFromDir(permanentPath)
		if err != nil {
			logger.WithError(err).Warn("could not read existing jobs from EFS")
		} else {
			for _, jobs := range existing.PresubmitsStatic {
				for _, j := range jobs {
					existingNames[j.Name] = true
				}
			}
			for _, jobs := range existing.PostsubmitsStatic {
				for _, j := range jobs {
					existingNames[j.Name] = true
				}
			}
			for _, j := range existing.Periodics {
				existingNames[j.Name] = true
			}
		}
	}

	for key := range allJobs.PresubmitsStatic {
		var filtered []prowconfig.Presubmit
		for _, j := range allJobs.PresubmitsStatic[key] {
			if !existingNames[j.Name] {
				filtered = append(filtered, j)
			}
		}
		allJobs.PresubmitsStatic[key] = filtered
	}
	for key := range allJobs.PostsubmitsStatic {
		var filtered []prowconfig.Postsubmit
		for _, j := range allJobs.PostsubmitsStatic[key] {
			if !existingNames[j.Name] {
				filtered = append(filtered, j)
			}
		}
		allJobs.PostsubmitsStatic[key] = filtered
	}
	var filteredPeriodics []prowconfig.Periodic
	for _, j := range allJobs.Periodics {
		if !existingNames[j.Name] {
			filteredPeriodics = append(filteredPeriodics, j)
		}
	}
	allJobs.Periodics = filteredPeriodics

	var jobNames []string
	for _, j := range allJobs.PresubmitsStatic[orgrepo] {
		jobNames = append(jobNames, j.Name)
	}
	for _, j := range allJobs.PostsubmitsStatic[orgrepo] {
		jobNames = append(jobNames, j.Name)
	}
	for _, j := range allJobs.Periodics {
		jobNames = append(jobNames, j.Name)
	}

	if len(jobNames) == 0 {
		s.ghc.CreateComment(org, repo, number, fmt.Sprintf("@%s: all tests from `.ci-operator/` configs already have permanent jobs on EFS. No new ephemeral jobs needed.", user))
		return
	}

	refs := &pjapi.Refs{
		Org:     org,
		Repo:    repo,
		BaseRef: branch,
		BaseSHA: pr.Base.SHA,
		Pulls:   []pjapi.Pull{{Number: number, Author: user, SHA: sha}},
	}

	var created []string
	for _, job := range allJobs.PresubmitsStatic[orgrepo] {
		pj := pjutil.NewProwJob(pjutil.PresubmitSpec(job, *refs), job.Labels, job.Annotations)
		pj.Namespace = s.namespace
		if err := s.pjclient.Create(context.Background(), &pj); err != nil {
			logger.WithError(err).WithField("job", job.Name).Error("could not create ProwJob")
			s.commentError(org, repo, number, user, "new-test", fmt.Errorf("could not create ProwJob %s: %w", job.Name, err), logger)
			return
		}
		created = append(created, job.Name)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "@%s: ProwJobs created from `.ci-operator/` configs (commit %s):\n\n", user, shortSHA(sha))
	for _, name := range created {
		fmt.Fprintf(&sb, "- `%s`\n", name)
	}
	sb.WriteString("\nJobs have been submitted directly and should appear shortly.")
	s.ghc.CreateComment(org, repo, number, sb.String())
	logger.WithField("jobs", len(created)).Info("ProwJobs created")
}

func (s *server) handlePullRequest(_ *logrus.Entry, _ github.PullRequestEvent) {
}

func (s *server) fetchConfigs(org, repo, sha string, l *logrus.Entry) (map[string]*cioperatorapi.ReleaseBuildConfiguration, error) {
	entries, err := s.ghc.GetDirectory(org, repo, ciOperatorDir, sha)
	if err != nil {
		l.WithError(err).Debug("could not list .ci-operator directory, trying single-file config")
		return s.fetchSingleConfig(org, repo, sha, l)
	}

	configs := map[string]*cioperatorapi.ReleaseBuildConfiguration{}
	for _, entry := range entries {
		if entry.Type != "file" {
			continue
		}
		if !strings.HasSuffix(entry.Name, ".yaml") && !strings.HasSuffix(entry.Name, ".yml") {
			continue
		}
		if !strings.HasPrefix(entry.Name, "ci-operator") {
			continue
		}

		content, err := s.ghc.GetFile(org, repo, entry.Path, sha)
		if err != nil {
			return nil, fmt.Errorf("could not fetch %s: %w", entry.Path, err)
		}
		if content == nil {
			l.WithField("file", entry.Path).Warn("file not found")
			continue
		}

		var cfg cioperatorapi.ReleaseBuildConfiguration
		if err := yaml.Unmarshal(content, &cfg); err != nil {
			return nil, fmt.Errorf("could not parse %s: %w", entry.Path, err)
		}
		configs[entry.Name] = &cfg
	}
	return configs, nil
}

// fetchSingleConfig handles the case where the repo uses a single .ci-operator.yaml
// file at the root instead of a .ci-operator/ directory.
func (s *server) fetchSingleConfig(org, repo, sha string, l *logrus.Entry) (map[string]*cioperatorapi.ReleaseBuildConfiguration, error) {
	const singleFile = ".ci-operator.yaml"
	content, err := s.ghc.GetFile(org, repo, singleFile, sha)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", singleFile, err)
	}
	if content == nil {
		return nil, nil
	}

	var cfg cioperatorapi.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("could not parse %s: %w", singleFile, err)
	}
	l.WithField("file", singleFile).Debug("using single-file config")
	return map[string]*cioperatorapi.ReleaseBuildConfiguration{
		"ci-operator.yaml": &cfg,
	}, nil
}

func (s *server) commentError(org, repo string, number int, user, command string, err error, l *logrus.Entry) {
	comment := fmt.Sprintf("@%s: `%s` error:\n```\n%v\n```", user, command, err)
	if commentErr := s.ghc.CreateComment(org, repo, number, comment); commentErr != nil {
		l.WithError(commentErr).Error("failed to create error comment")
	}
}

func metadataFromFilename(filename, org, repo, branch string) *cioperatorapi.Metadata {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	var variant string
	if _, after, found := strings.Cut(base, "__"); found {
		variant = after
	}
	return &cioperatorapi.Metadata{
		Org:     org,
		Repo:    repo,
		Branch:  branch,
		Variant: variant,
	}
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
