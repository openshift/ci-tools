package config

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"
)

const (
	// ConfigInRepoPath is the prow config path from release repo
	ConfigInRepoPath = "core-services/prow/02_config/_config.yaml"
	// ProwConfigFile is the filename where Prow config lives
	ProwConfigFile = "_config.yaml"
	// PluginConfigFile is the filename where plugins live
	PluginConfigFile = "_plugins.yaml"
	// PluginConfigInRepoPath is the prow plugin config path from release repo
	PluginConfigInRepoPath = "core-services/prow/02_config/" + PluginConfigFile
	// JobConfigInRepoPath is the prowjobs path from release repo
	JobConfigInRepoPath = "ci-operator/jobs"
	// CiopConfigInRepoPath is the ci-operator config path from release repo
	CiopConfigInRepoPath = "ci-operator/config"
	// TemplatesPath is the path of the templates from release repo
	TemplatesPath = "ci-operator/templates"
	// TemplatePrefix is the prefix added to ConfigMap names
	TemplatePrefix = "prow-job-"
	// ClusterProfilesPath is where profiles are stored in the release repo
	ClusterProfilesPath = "cluster/test-deploy"
	// ClusterProfilePrefix is the prefix added to ConfigMap names
	ClusterProfilePrefix = "cluster-profile-"
	// StagingNamespace is the staging namespace in api.ci
	StagingNamespace = "ci-stg"
)

// ReleaseRepoConfig contains all configuration present in release repo (usually openshift/release)
type ReleaseRepoConfig struct {
	Prow       *prowconfig.Config
	CiOperator ByFilename
}

func git(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("'%s' failed with error=%v, output:\n%s", cmd.Args, err, out)
	}
	return string(out), nil
}

func revParse(repoPath string, args ...string) (string, error) {
	out, err := git(repoPath, append([]string{"rev-parse"}, args...)...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func gitCheckout(candidatePath, baseSHA string) error {
	cmd := exec.Command("git", "checkout", baseSHA)
	cmd.Dir = candidatePath
	stdoutStderr, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("'%s' failed with out: %s and error %v", cmd.Args, stdoutStderr, err)
	}
	return nil
}

// NewLocalJobSpec creates a fake JobSpec based on information extracted from
// the local git repository to simulate a CI job.
func NewLocalJobSpec(path string) (*pjdwapi.JobSpec, error) {
	refs := pjapi.Refs{
		Org:   "openshift",
		Repo:  "release",
		Pulls: []pjapi.Pull{{}},
	}
	var err error
	if refs.Pulls[0].Ref, err = revParse(path, "--abbrev-ref", "HEAD"); err != nil {
		return nil, fmt.Errorf("could not get current branch: %v", err)
	}
	if refs.BaseRef, err = revParse(path, "--abbrev-ref", refs.Pulls[0].Ref+"@{upstream}"); err != nil {
		logrus.WithError(err).Info("current branch has no upstream, using `master`")
		refs.BaseRef = "master"
	}
	if refs.BaseSHA, err = revParse(path, refs.BaseRef); err != nil {
		return nil, fmt.Errorf("could not parse base revision: %v", err)
	}
	if refs.Pulls[0].SHA, err = revParse(path, refs.Pulls[0].Ref); err != nil {
		return nil, fmt.Errorf("could not parse pull revision: %v", err)
	}
	return &pjdwapi.JobSpec{Type: pjapi.PresubmitJob, Refs: &refs}, nil
}

// GetAllConfigs loads all configuration from the working copy of the release repo (usually openshift/release).
// When an error occurs during some config loading, the error is not propagated, but the returned struct field will
// have a nil value in the appropriate field. The error is only logged.
func GetAllConfigs(releaseRepoPath string, logger *logrus.Entry) *ReleaseRepoConfig {
	config := &ReleaseRepoConfig{}
	var err error
	ciopConfigPath := filepath.Join(releaseRepoPath, CiopConfigInRepoPath)
	config.CiOperator, err = LoadConfigByFilename(ciopConfigPath)
	if err != nil {
		logger.WithError(err).Warn("failed to load ci-operator configuration from release repo")
	}

	prowConfigPath := filepath.Join(releaseRepoPath, ConfigInRepoPath)
	prowJobConfigPath := filepath.Join(releaseRepoPath, JobConfigInRepoPath)
	config.Prow, err = prowconfig.Load(prowConfigPath, prowJobConfigPath)
	if err != nil {
		logger.WithError(err).Warn("failed to load Prow configuration from release repo")
	}

	return config
}

// GetAllConfigsFromSHA loads all configuration from given SHA revision of the release repo (usually openshift/release).
// This method checks out the given revision before the configuration is loaded, and then checks out back the saved
// revision that was checked out in the working copy when this method was called. Errors occurred during these git
// manipulations are propagated in the error return value. Errors occurred during the actual config loading are not
// propagated, but the returned struct field will have a nil value in the appropriate field. The error is only logged.
func GetAllConfigsFromSHA(releaseRepoPath, sha string, logger *logrus.Entry) (*ReleaseRepoConfig, error) {
	currentSHA, err := revParse(releaseRepoPath, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get SHA of current HEAD: %v", err)
	}
	restoreRev, err := revParse(releaseRepoPath, "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get current branch: %v", err)
	}
	if restoreRev == "HEAD" {
		restoreRev = currentSHA
	}
	if err := gitCheckout(releaseRepoPath, sha); err != nil {
		return nil, fmt.Errorf("could not checkout worktree: %v", err)
	}

	config := GetAllConfigs(releaseRepoPath, logger)

	if err := gitCheckout(releaseRepoPath, restoreRev); err != nil {
		return config, fmt.Errorf("failed to check out tested revision back: %v", err)
	}

	return config, nil
}

func GetChangedTemplates(path, baseRev string) ([]ConfigMapSource, error) {
	changes, err := getRevChanges(path, TemplatesPath, baseRev, true)
	if err != nil {
		return nil, err
	}
	var ret []ConfigMapSource
	for _, c := range changes {
		if filepath.Ext(c.Filename) == ".yaml" {
			ret = append(ret, c)
		}
	}
	return ret, nil
}

func GetChangedClusterProfiles(path, baseRev string) ([]ConfigMapSource, error) {
	return getRevChanges(path, ClusterProfilesPath, baseRev, false)
}

// getRevChanges returns the name and a hash of the contents of files under
// `path` that were added/modified since revision `base` in the repository at
// `root`.  Paths are relative to `root`.
func getRevChanges(root, path, base string, rec bool) ([]ConfigMapSource, error) {
	// Sample output (with abbreviated hashes) from git-diff-tree(1):
	// :100644 100644 bcd1234 0123456 M file0
	cmd := []string{"diff-tree", "--diff-filter=ABCMRTUX", base + ":" + path, "HEAD:" + path}
	if rec {
		cmd = append(cmd, "-r")
	}
	diff, err := git(root, cmd...)
	if err != nil || diff == "" {
		return nil, err
	}
	var ret []ConfigMapSource
	for _, l := range strings.Split(strings.TrimSpace(diff), "\n") {
		ret = append(ret, ConfigMapSource{
			Filename: filepath.Join(path, l[99:]),
			SHA:      l[56:96],
		})
	}
	return ret, nil
}
