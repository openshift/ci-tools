package config

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattn/go-zglob"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/plugins"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/registry"
)

const (
	// ConfigInRepoPath is the prow config path from release repo
	ConfigInRepoPath = "core-services/prow/02_config/_config.yaml"
	// ProwConfigFile is the filename where Prow config lives
	ProwConfigFile = "_config.yaml"
	// SupplementalProwConfigFileName is the name of supplemental prow config files.
	SupplementalProwConfigFileName = "_prowconfig.yaml"
	// SupplementalPluginConfigFileName is the name of supplemental  plugin config
	// files.
	SupplementalPluginConfigFileName = "_pluginconfig.yaml"
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
	// ClusterProfilesPath is where profiles are stored in the release repo
	ClusterProfilesPath = "cluster/test-deploy"
	// StagingNamespace is the staging namespace in api.ci
	StagingNamespace = "ci-stg"
	// RegistryPath is the path to the multistage step registry
	RegistryPath = "ci-operator/step-registry"
)

// ConfigMapName returns the name of the ConfigMap to which config-updater would
// put the content, and the config-updater config pattern that covers the path
// of the ConfigMap source.
func ConfigMapName(path string, updater plugins.ConfigUpdater) (name, pattern string, err error) {
	for pattern, cfg := range updater.Maps {
		if match, err := zglob.Match(pattern, path); match || err != nil {
			return cfg.Name, pattern, err
		}
	}

	return "", "", fmt.Errorf("path not covered by any config-updater pattern: %s", path)
}

// ReleaseRepoConfig contains all configuration present in release repo (usually openshift/release)
type ReleaseRepoConfig struct {
	Prow       *prowconfig.Config
	CiOperator DataByFilename
}

func git(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("'%s' failed with error=%w, output:\n%s", cmd.Args, err, out)
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
		return fmt.Errorf("'%s' failed with out: %s and error %w", cmd.Args, stdoutStderr, err)
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
		return nil, fmt.Errorf("could not get current branch: %w", err)
	}
	if refs.BaseRef, err = revParse(path, "--abbrev-ref", refs.Pulls[0].Ref+"@{upstream}"); err != nil {
		logrus.WithError(err).Info("current branch has no upstream, using `master`")
		refs.BaseRef = "master"
	}
	if refs.BaseSHA, err = revParse(path, refs.BaseRef); err != nil {
		return nil, fmt.Errorf("could not parse base revision: %w", err)
	}
	if refs.Pulls[0].SHA, err = revParse(path, refs.Pulls[0].Ref); err != nil {
		return nil, fmt.Errorf("could not parse pull revision: %w", err)
	}
	return &pjdwapi.JobSpec{Type: pjapi.PresubmitJob, Refs: &refs}, nil
}

// GetAllConfigs loads all configuration from the working copy of the release repo (usually openshift/release).
// When an error occurs during some config loading, the error will be propagated, however the returned struct field will
// also have a nil value in the appropriate field.
func GetAllConfigs(releaseRepoPath string) (*ReleaseRepoConfig, error) {
	config := &ReleaseRepoConfig{}
	var errs []error
	var err error
	ciopConfigPath := filepath.Join(releaseRepoPath, CiopConfigInRepoPath)
	config.CiOperator, err = LoadDataByFilename(ciopConfigPath)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to load ci-operator configuration from release repo: %w", err))
	}

	prowConfigPath := filepath.Join(releaseRepoPath, ConfigInRepoPath)
	prowJobConfigPath := filepath.Join(releaseRepoPath, JobConfigInRepoPath)
	config.Prow, err = prowconfig.Load(prowConfigPath, prowJobConfigPath, nil, "")
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to load Prow configuration from release repo: %w", err))
	}

	return config, utilerrors.NewAggregate(errs)
}

// GetAllConfigsFromSHA loads all configuration from given SHA revision of the release repo (usually openshift/release).
// This method checks out the given revision before the configuration is loaded, and then checks out back the saved
// revision that was checked out in the working copy when this method was called. Errors occurred during these git
// manipulations are propagated in the error return value. Errors occurred during the actual config loading are not
// propagated, but the returned struct field will have a nil value in the appropriate field. The error is only logged.
func GetAllConfigsFromSHA(releaseRepoPath, sha string) (*ReleaseRepoConfig, error) {
	currentSHA, err := revParse(releaseRepoPath, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get SHA of current HEAD: %w", err)
	}
	restoreRev, err := revParse(releaseRepoPath, "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get current branch: %w", err)
	}
	if restoreRev == "HEAD" {
		restoreRev = currentSHA
	}
	if err := gitCheckout(releaseRepoPath, sha); err != nil {
		return nil, fmt.Errorf("could not checkout worktree: %w", err)
	}

	var errs []error
	config, err := GetAllConfigs(releaseRepoPath)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to get all configs: %w", err))
	}

	if err = gitCheckout(releaseRepoPath, restoreRev); err != nil {
		errs = append(errs, fmt.Errorf("failed to check out tested revision back: %w", err))
	}

	return config, utilerrors.NewAggregate(errs)
}

func GetChangedTemplates(path, baseRev string) ([]string, error) {
	changes, err := getRevChanges(path, TemplatesPath, baseRev, false)
	if err != nil {
		return nil, err
	}
	var changedTemplates []string
	for _, c := range changes {
		if filepath.Ext(c) == ".yaml" {
			changedTemplates = append(changedTemplates, c)
		}
	}
	return changedTemplates, nil
}

func loadRegistryStep(filename string, graph registry.NodeByName) (registry.Node, error) {
	// if a commands script changed, mark reference as changed
	var type_, name string
	var node registry.Node
	var ok bool
	switch {
	case strings.HasSuffix(filename, registry.RefSuffix):
		type_, name = "ref", strings.TrimSuffix(filename, registry.RefSuffix)
		node, ok = graph.References[name]
	case strings.HasSuffix(filename, registry.ObserverSuffix):
		type_, name = "observer", strings.TrimSuffix(filename, registry.ObserverSuffix)
		node, ok = graph.Observers[name]
	case strings.HasSuffix(filename, registry.ChainSuffix):
		type_, name = "chain", strings.TrimSuffix(filename, registry.ChainSuffix)
		node, ok = graph.Chains[name]
	case strings.HasSuffix(filename, registry.WorkflowSuffix):
		type_, name = "workflow", strings.TrimSuffix(filename, registry.WorkflowSuffix)
		node, ok = graph.Workflows[name]
	case strings.Contains(filename, registry.CommandsSuffix):
		extension := filepath.Ext(filename)
		type_, name = "ref", strings.TrimSuffix(filename[0:len(filename)-len(extension)], registry.CommandsSuffix)
		if node, ok = graph.References[name]; !ok {
			node, ok = graph.Observers[name]
		}
	default:
		return nil, fmt.Errorf("invalid step registry filename: %s", filename)
	}
	if !ok {
		return nil, fmt.Errorf("could not find registry component in registry graph: %s/%s", type_, name)
	}
	return node, nil
}

// GetChangedRegistrySteps identifies all registry components that changed.
func GetChangedRegistrySteps(path, baseRev string, graph registry.NodeByName) ([]registry.Node, error) {
	var changes []registry.Node
	revChanges, err := getRevChanges(path, RegistryPath, baseRev, false)
	if err != nil {
		return changes, err
	}
	for _, c := range revChanges {
		if filepath.Ext(c) == ".yaml" || strings.HasSuffix(c, fmt.Sprintf("%s%s", registry.CommandsSuffix, filepath.Ext(c))) {
			node, err := loadRegistryStep(filepath.Base(c), graph)
			if err != nil {
				return changes, err
			}
			changes = append(changes, node)
		}
	}
	return changes, nil
}

func GetChangedClusterProfiles(path, baseRev string) ([]string, error) {
	return getRevChanges(path, ClusterProfilesPath, baseRev, false)
}

func GetAddedConfigs(path, baseRev string) ([]string, error) {
	return getRevChanges(path, CiopConfigInRepoPath, baseRev, true)
}

// getRevChanges returns the name and a hash of the contents of files under
// `path` that were added/modified since revision `base` in the repository at
// `root`.  Paths are relative to `root`.
// If 'ignoreModified' is true it will only check for relevant added, moved, or copied files
func getRevChanges(root, path, base string, ignoreModified bool) ([]string, error) {
	// Sample output (with abbreviated hashes) from git-diff-tree(1):
	// :100644 100644 bcd1234 0123456 M file0
	filter := "--diff-filter=d"
	if ignoreModified {
		filter = "--diff-filter=ACR"
	}
	cmd := []string{"diff-tree", "-r", filter, base + ":" + path, "HEAD:" + path}
	diff, err := git(root, cmd...)
	if err != nil || diff == "" {
		return nil, err
	}
	var ret []string
	for _, l := range strings.Split(strings.TrimSpace(diff), "\n") {
		ret = append(ret, filepath.Join(path, l[99:]))
	}
	return ret, nil
}

// LoadProwConfig loads Prow configuration from the release repo
func LoadProwConfig(releaseRepo string) (*prowconfig.Config, error) {
	agent := prowconfig.Agent{}
	if err := agent.Start(filepath.Join(releaseRepo, ConfigInRepoPath), "", []string{filepath.Dir(filepath.Join(releaseRepo, ConfigInRepoPath))}, SupplementalProwConfigFileName); err != nil {
		return nil, fmt.Errorf("could not load Prow configuration: %w", err)
	}
	return agent.Config(), nil
}

// ProwConfigForOrgRepo returns the Prow configuration file for the org/repo
func ProwConfigForOrgRepo(releaseRepo, org, repo string) string {
	return filepath.Join(filepath.Join(filepath.Dir(filepath.Join(releaseRepo, ConfigInRepoPath)), org, repo), SupplementalProwConfigFileName)
}
