package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/yaml"
	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
)

const ciOperatorDir = ".ci-operator"

var pluginGenerator = jc.Generator(pluginName)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetDirectory(org, repo, dirpath, commit string) ([]github.DirectoryContent, error)
	GetFile(org, repo, filepath, commit string) ([]byte, error)
}

type server struct {
	ghc              githubClient
	pjclient         ctrlruntimeclient.Client
	prowConfigPath   string
	namespace        string
	jobConfigDir     string
	prowgenImage     string
	checkconfigImage string
	efsLocks         sync.Map
}

func (s *server) repoLock(org, repo string) *sync.Mutex {
	key := org + "/" + repo
	val, _ := s.efsLocks.LoadOrStore(key, &sync.Mutex{})
	return val.(*sync.Mutex)
}

func helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	return &pluginhelp.PluginHelp{
		Description: "The in-repo-config plugin automatically manages CI jobs for repos using in-repo .ci-operator/ configuration. " +
			"It writes ephemeral ProwJob definitions for new tests on PRs, " +
			"writes permanent definitions on merge, and auto-onboards repos on first push.",
	}, nil
}

func (s *server) handlePullRequest(l *logrus.Entry, pre github.PullRequestEvent) {
	org := pre.Repo.Owner.Login
	repo := pre.Repo.Name
	number := pre.Number
	logger := l.WithFields(logrus.Fields{
		"org": org, "repo": repo, "pr": number, "action": pre.Action,
	})

	switch pre.Action {
	case github.PullRequestActionOpened, github.PullRequestActionSynchronize, github.PullRequestActionReopened:
		s.handlePROpenedOrUpdated(logger, pre)
	case github.PullRequestActionClosed:
		s.handlePRClosed(logger, pre)
	}
}

func (s *server) handlePROpenedOrUpdated(logger *logrus.Entry, pre github.PullRequestEvent) {
	org := pre.Repo.Owner.Login
	repo := pre.Repo.Name
	number := pre.Number
	sha := pre.PullRequest.Head.SHA
	branch := pre.PullRequest.Base.Ref
	orgrepo := org + "/" + repo

	allJobs, _, err := s.generateAllJobs(org, repo, branch, sha, logger)
	if err != nil {
		logger.WithError(err).Error("could not generate jobs")
		s.comment(logger, org, repo, number,
			fmt.Sprintf("Error generating jobs at commit %s. Please check plugin logs.", shortSHA(sha)))
		return
	}
	if allJobs == nil {
		return
	}

	newJobNames, newPresubmits, newPeriodics := filterNewJobs(allJobs, s.jobConfigDir, org, repo, logger)
	if len(newJobNames) == 0 {
		logger.Info("no new tests detected, skipping ephemeral write")
		return
	}

	lock := s.repoLock(org, repo)
	lock.Lock()
	defer lock.Unlock()

	ephemeralDir := filepath.Join(s.jobConfigDir, "ephemeral", org, repo, fmt.Sprintf("PR-%d", number))
	if err := os.RemoveAll(ephemeralDir); err != nil {
		logger.WithError(err).Warn("could not clean old ephemeral directory")
	}

	ephemeralJobs := &prowconfig.JobConfig{
		PresubmitsStatic:  map[string][]prowconfig.Presubmit{orgrepo: newPresubmits},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
		Periodics:         newPeriodics,
	}
	if err := writeEphemeralJobs(ephemeralDir, org, repo, number, ephemeralJobs); err != nil {
		logger.WithError(err).Error("could not write ephemeral jobs to EFS")
		s.comment(logger, org, repo, number, "Error writing ephemeral job definitions. Please check plugin logs.")
		return
	}

	triggered := s.triggerNewJobs(logger, pre, orgrepo, newPresubmits, newPeriodics)

	var sb strings.Builder
	fmt.Fprintf(&sb, "New tests detected in `.ci-operator/` configs (commit %s):\n\n", shortSHA(sha))
	for _, j := range newPresubmits {
		fmt.Fprintf(&sb, "- `%s`\n", j.Name)
	}
	for _, j := range newPeriodics {
		fmt.Fprintf(&sb, "- `%s` (periodic, triggered as presubmit for validation)\n", j.Name)
	}
	if len(triggered) > 0 {
		sb.WriteString("\nTriggered automatically. Ephemeral definitions written to EFS — use `/test <name>` to re-run.")
	}
	sb.WriteString(" Jobs will be cleaned up when this PR is closed.")
	s.comment(logger, org, repo, number, sb.String())
	logger.WithField("jobs", len(newJobNames)).WithField("triggered", len(triggered)).Info("ephemeral jobs written and triggered")
}

func (s *server) triggerNewJobs(logger *logrus.Entry, pre github.PullRequestEvent, orgrepo string, presubmits []prowconfig.Presubmit, periodics []prowconfig.Periodic) []string {
	org := pre.Repo.Owner.Login
	repo := pre.Repo.Name
	branch := pre.PullRequest.Base.Ref
	sha := pre.PullRequest.Head.SHA

	prowCfg, err := prowconfig.Load(s.prowConfigPath, "", nil, "")
	if err != nil {
		logger.WithError(err).Error("could not load prow config for ProwJob defaults")
		return nil
	}

	refs := pjapi.Refs{
		Org:     org,
		Repo:    repo,
		BaseRef: branch,
		BaseSHA: pre.PullRequest.Base.SHA,
		Pulls:   []pjapi.Pull{{Number: pre.Number, Author: pre.PullRequest.User.Login, SHA: sha}},
	}

	var triggered []string
	for _, job := range presubmits {
		applyJobDefaults(&job.JobBase, orgrepo, prowCfg)
		pj := pjutil.NewProwJob(pjutil.PresubmitSpec(job, refs), job.Labels, job.Annotations)
		pj.Namespace = s.namespace
		if err := s.pjclient.Create(context.Background(), &pj); err != nil {
			logger.WithError(err).WithField("job", job.Name).Error("could not create ProwJob")
			continue
		}
		triggered = append(triggered, job.Name)
	}

	for _, job := range periodics {
		var extraRefs []pjapi.Refs
		for _, ref := range job.ExtraRefs {
			if ref.Org == org && ref.Repo == repo {
				continue
			}
			extraRefs = append(extraRefs, ref)
		}
		job.ExtraRefs = extraRefs
		testName := periodicTestName(job.Name, org, repo, branch)
		presubmit := prowconfig.Presubmit{
			JobBase:  job.JobBase,
			Reporter: prowconfig.Reporter{Context: fmt.Sprintf("ci/prow/%s", testName)},
		}
		applyJobDefaults(&presubmit.JobBase, orgrepo, prowCfg)
		pj := pjutil.NewProwJob(pjutil.PresubmitSpec(presubmit, refs), presubmit.Labels, presubmit.Annotations)
		pj.Namespace = s.namespace
		if err := s.pjclient.Create(context.Background(), &pj); err != nil {
			logger.WithError(err).WithField("job", job.Name).Error("could not create ProwJob for periodic")
			continue
		}
		triggered = append(triggered, job.Name)
	}
	return triggered
}

func applyJobDefaults(job *prowconfig.JobBase, orgrepo string, prowCfg *prowconfig.Config) {
	if job.Cluster == "" {
		job.Cluster = kube.DefaultClusterAlias
	}
	if job.Namespace == nil || *job.Namespace == "" {
		ns := prowCfg.PodNamespace
		job.Namespace = &ns
	}
	if dc := prowCfg.Plank.GuessDefaultDecorationConfig(orgrepo, job.Cluster); dc != nil {
		if job.DecorationConfig != nil {
			job.DecorationConfig = dc.ApplyDefault(job.DecorationConfig)
		} else {
			job.DecorationConfig = dc
		}
	}
}

func (s *server) handlePRClosed(logger *logrus.Entry, pre github.PullRequestEvent) {
	org := pre.Repo.Owner.Login
	repo := pre.Repo.Name
	number := pre.Number

	lock := s.repoLock(org, repo)
	lock.Lock()
	defer lock.Unlock()

	ephemeralDir := filepath.Join(s.jobConfigDir, "ephemeral", org, repo, fmt.Sprintf("PR-%d", number))
	if err := os.RemoveAll(ephemeralDir); err != nil {
		logger.WithError(err).Error("could not clean up ephemeral jobs")
	} else {
		logger.Info("cleaned up ephemeral jobs for closed PR")
	}
}

func (s *server) handlePush(l *logrus.Entry, pe github.PushEvent) {
	if pe.Deleted || !strings.HasPrefix(pe.Ref, "refs/heads/") {
		return
	}

	org := pe.Repo.Owner.Login
	repo := pe.Repo.Name
	branch := pe.Branch()
	sha := pe.After
	logger := l.WithFields(logrus.Fields{
		"org": org, "repo": repo, "branch": branch, "sha": sha,
	})

	if !pushTouchesCIOperator(pe) {
		return
	}

	if pe.Repo.DefaultBranch != "" && branch != pe.Repo.DefaultBranch {
		logger.Debug("ignoring push to non-default branch")
		return
	}

	logger.Info("push touches .ci-operator/ configs, generating permanent jobs")

	allJobs, useDir, err := s.generateAllJobs(org, repo, branch, sha, logger)
	if err != nil {
		logger.WithError(err).Error("could not fetch or generate jobs from pushed commit")
		return
	}
	if allJobs == nil {
		logger.Warn("push touched .ci-operator/ but no configs found")
		return
	}

	lock := s.repoLock(org, repo)
	lock.Lock()
	defer lock.Unlock()

	params := newBootstrapParams(org, repo, branch, useDir, s.prowgenImage, s.checkconfigImage)
	bootstrapJobs, err := generateBootstrapJobs(params)
	if err != nil {
		logger.WithError(err).Error("could not generate bootstrap jobs")
		return
	}
	jc.Append(allJobs, bootstrapJobs)

	if err := jc.WriteBranchToDir(s.jobConfigDir, org, repo, allJobs, pluginGenerator); err != nil {
		logger.WithError(err).Error("could not write jobs to EFS")
		return
	}
	logger.Info("jobs written to EFS")
}

func pushTouchesCIOperator(pe github.PushEvent) bool {
	for _, commit := range pe.Commits {
		for _, files := range [][]string{commit.Added, commit.Modified, commit.Removed} {
			for _, f := range files {
				if strings.HasPrefix(f, ciOperatorDir+"/") || f == ".ci-operator.yaml" {
					return true
				}
			}
		}
	}
	return false
}

func (s *server) generateAllJobs(org, repo, branch, sha string, logger *logrus.Entry) (*prowconfig.JobConfig, bool, error) {
	configs, useDir, err := s.fetchConfigs(org, repo, sha, logger)
	if err != nil {
		return nil, false, err
	}
	if len(configs) == 0 {
		return nil, false, nil
	}

	allJobs := &prowconfig.JobConfig{
		PresubmitsStatic:  map[string][]prowconfig.Presubmit{},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
	}
	for filename, configSpec := range configs {
		info := metadataFromFilename(filename, org, repo, branch)
		configSpec.UnresolvedConfigPath = cioperatorapi.CIOperatorInrepoConfigFileName
		generated, err := prowgen.GenerateJobs(configSpec, info)
		if err != nil {
			return nil, false, fmt.Errorf("prowgen failed for %s: %w", filename, err)
		}
		jc.Append(allJobs, generated)
	}
	return allJobs, useDir, nil
}

// writeEphemeralJobs writes a single job config file with a PR-prefixed basename
// to avoid collisions with permanent files. Prow rejects duplicate basenames
// across the recursive job-config-path walk.
func writeEphemeralJobs(ephemeralDir, org, repo string, prNumber int, allJobs *prowconfig.JobConfig) error {
	if err := os.MkdirAll(ephemeralDir, os.ModePerm); err != nil {
		return fmt.Errorf("could not create ephemeral directory: %w", err)
	}
	filename := fmt.Sprintf("pr%d-%s-%s-ephemeral.yaml", prNumber, org, repo)
	return jc.WriteToFile(filepath.Join(ephemeralDir, filename), allJobs)
}

func filterNewJobs(allJobs *prowconfig.JobConfig, jobConfigDir, org, repo string, logger *logrus.Entry) ([]string, []prowconfig.Presubmit, []prowconfig.Periodic) {
	existingNames := map[string]bool{}
	permanentPath := filepath.Join(jobConfigDir, org, repo)
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

	var newJobNames []string
	var newPresubmits []prowconfig.Presubmit
	var newPeriodics []prowconfig.Periodic
	orgrepo := fmt.Sprintf("%s/%s", org, repo)
	for _, j := range allJobs.PresubmitsStatic[orgrepo] {
		if !existingNames[j.Name] {
			newJobNames = append(newJobNames, j.Name)
			newPresubmits = append(newPresubmits, j)
		}
	}
	for _, j := range allJobs.PostsubmitsStatic[orgrepo] {
		if !existingNames[j.Name] {
			newJobNames = append(newJobNames, j.Name)
		}
	}
	for _, j := range allJobs.Periodics {
		if !existingNames[j.Name] {
			newJobNames = append(newJobNames, j.Name)
			newPeriodics = append(newPeriodics, j)
		}
	}
	return newJobNames, newPresubmits, newPeriodics
}

func (s *server) fetchConfigs(org, repo, sha string, l *logrus.Entry) (map[string]*cioperatorapi.ReleaseBuildConfiguration, bool, error) {
	entries, err := s.ghc.GetDirectory(org, repo, ciOperatorDir, sha)
	if err != nil {
		configs, err := s.fetchSingleConfig(org, repo, sha, l)
		return configs, false, err
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
			return nil, false, fmt.Errorf("could not fetch %s: %w", entry.Path, err)
		}
		if content == nil {
			l.WithField("file", entry.Path).Warn("file not found")
			continue
		}

		var cfg cioperatorapi.ReleaseBuildConfiguration
		if err := yaml.Unmarshal(content, &cfg); err != nil {
			return nil, false, fmt.Errorf("could not parse %s: %w", entry.Path, err)
		}
		configs[entry.Name] = &cfg
	}
	return configs, true, nil
}

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
	return map[string]*cioperatorapi.ReleaseBuildConfiguration{
		"ci-operator.yaml": &cfg,
	}, nil
}

func (s *server) comment(l *logrus.Entry, org, repo string, number int, msg string) {
	if err := s.ghc.CreateComment(org, repo, number, msg); err != nil {
		l.WithError(err).Error("failed to post comment")
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

func periodicTestName(jobName, org, repo, branch string) string {
	prefix := fmt.Sprintf("periodic-ci-%s-%s-%s-", org, repo, branch)
	if name, ok := strings.CutPrefix(jobName, prefix); ok {
		return name
	}
	return jobName
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


func (s *server) startEphemeralGC(ctx context.Context, interval time.Duration, logger *logrus.Entry) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.gcEphemeralDirs(logger)
		}
	}
}

func (s *server) gcEphemeralDirs(logger *logrus.Entry) {
	ephemeralRoot := filepath.Join(s.jobConfigDir, "ephemeral")
	if !dirExists(ephemeralRoot) {
		return
	}

	for _, entry := range s.listEphemeralPRDirs(ephemeralRoot, logger) {
		pr, err := s.ghc.GetPullRequest(entry.org, entry.repo, entry.prNum)
		if err != nil {
			logger.WithError(err).WithFields(logrus.Fields{
				"org": entry.org, "repo": entry.repo, "pr": entry.prNum,
			}).Warn("could not check PR state for GC")
			continue
		}
		if pr.State != "closed" {
			continue
		}
		lock := s.repoLock(entry.org, entry.repo)
		lock.Lock()
		err = os.RemoveAll(entry.path)
		lock.Unlock()
		if err != nil {
			logger.WithError(err).WithField("path", entry.path).Warn("could not remove stale ephemeral directory")
		} else {
			logger.WithFields(logrus.Fields{
				"org": entry.org, "repo": entry.repo, "pr": entry.prNum,
			}).Info("GC removed stale ephemeral directory for closed PR")
		}
	}
}

type ephemeralPRDir struct {
	org, repo string
	prNum     int
	path      string
}

func (s *server) listEphemeralPRDirs(root string, logger *logrus.Entry) []ephemeralPRDir {
	var result []ephemeralPRDir
	orgs, err := os.ReadDir(root)
	if err != nil {
		logger.WithError(err).Warn("could not list ephemeral directory for GC")
		return nil
	}
	for _, orgEntry := range orgs {
		if !orgEntry.IsDir() {
			continue
		}
		repos, _ := os.ReadDir(filepath.Join(root, orgEntry.Name()))
		for _, repoEntry := range repos {
			if !repoEntry.IsDir() {
				continue
			}
			prDirs, _ := os.ReadDir(filepath.Join(root, orgEntry.Name(), repoEntry.Name()))
			for _, prDir := range prDirs {
				if !prDir.IsDir() || !strings.HasPrefix(prDir.Name(), "PR-") {
					continue
				}
				prNum, err := strconv.Atoi(strings.TrimPrefix(prDir.Name(), "PR-"))
				if err != nil {
					continue
				}
				result = append(result, ephemeralPRDir{
					org:   orgEntry.Name(),
					repo:  repoEntry.Name(),
					prNum: prNum,
					path:  filepath.Join(root, orgEntry.Name(), repoEntry.Name(), prDir.Name()),
				})
			}
		}
	}
	return result
}
