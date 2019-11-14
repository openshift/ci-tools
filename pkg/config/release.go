package config

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	registryutil "github.com/openshift/ci-tools/pkg/registry"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
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
	// RegistryPath is the path to the multistage step registry
	RegistryPath = "ci-operator/step-registry"
)

// ReleaseRepoConfig contains all configuration present in release repo (usually openshift/release)
type ReleaseRepoConfig struct {
	Prow       *prowconfig.Config
	CiOperator ByFilename
}

// RegistryStep includes the 3 types of components of the multistage registry as pointers
// and is used to store all registry changes using a single struct that can be put into a slice.
type RegistryStep struct {
	Reference *api.RegistryReference
	Chain     *api.RegistryChain
	Workflow  *api.RegistryWorkflow
}

type registry struct {
	refs      map[string]api.LiteralTestStep
	chains    map[string][]api.TestStep
	workflows map[string]api.MultiStageTestConfiguration
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

func loadRegistryStep(filename string, fullRegistry registry) (RegistryStep, error) {
	if strings.HasSuffix(filename, "-commands.sh") {
		filename = strings.TrimSuffix(filename, "-commands.sh")
		filename = strings.Join([]string{filename, "-ref.yaml"}, "")
	}
	if strings.HasSuffix(filename, "-ref.yaml") {
		name := strings.TrimSuffix(filename, "-ref.yaml")
		ref, ok := fullRegistry.refs[name]
		if !ok {
			return RegistryStep{}, fmt.Errorf("could not find reference: %s", name)
		}
		change := RegistryStep{
			Reference: &api.RegistryReference{
				LiteralTestStep: ref,
			},
		}
		return change, nil
	}
	if strings.HasSuffix(filename, "-chain.yaml") {
		name := strings.TrimSuffix(filename, "-chain.yaml")
		chain, ok := fullRegistry.chains[name]
		if !ok {
			return RegistryStep{}, fmt.Errorf("could not find chain: %s", name)
		}
		change := RegistryStep{
			Chain: &api.RegistryChain{
				As:    name,
				Steps: chain,
			},
		}
		return change, nil
	}
	if strings.HasSuffix(filename, "-workflow.yaml") {
		name := strings.TrimSuffix(filename, "-workflow.yaml")
		workflow, ok := fullRegistry.workflows[name]
		if !ok {
			return RegistryStep{}, fmt.Errorf("could not find workflow: %s", name)
		}
		change := RegistryStep{
			Workflow: &api.RegistryWorkflow{
				As:    name,
				Steps: workflow,
			},
		}
		return change, nil
	}
	return RegistryStep{}, fmt.Errorf("invalid step filename: %s", filename)
}

// GetChangedRegistrySteps identifies all registry components (refs, chains, and workflows) that changed.
// After all changes have been identified, the list gets "trimmed": all changed refs that are included in
// changed chains are removed from the list since the refs will be tested when the chain gets tested. This
// is also done for changed chains and refs included in changed workflows. This helps reduce the number of
// rehearsals that are performed.
func GetChangedRegistrySteps(path, baseRev string, refs map[string]api.LiteralTestStep, chains map[string][]api.TestStep, workflows map[string]api.MultiStageTestConfiguration) ([]RegistryStep, error) {
	revChanges, err := getRevChanges(path, RegistryPath, baseRev, true)
	if err != nil {
		return nil, err
	}
	var registryChanges []RegistryStep
	if err != nil {
		return nil, fmt.Errorf("Failed to load registry: %v", err)
	}
	fullRegistry := registry{refs: refs, chains: chains, workflows: workflows}
	seenReference := make(map[string]bool)
	for _, c := range revChanges {
		if filepath.Ext(c.Filename) == ".yaml" || strings.HasSuffix(c.Filename, "-commands.sh") {
			change, err := loadRegistryStep(filepath.Base(c.Filename), fullRegistry)
			if err != nil {
				return nil, err
			}
			if change.Reference != nil {
				if _, ok := seenReference[change.Reference.As]; ok {
					continue
				}
				seenReference[change.Reference.As] = true
			}
			registryChanges = append(registryChanges, change)
		}
	}
	trimmedChanges := trimRegistryChanges(registryChanges, fullRegistry)
	return trimmedChanges, nil
}

// getNestedChainsFromSteps returns the names of all chains nested inside other chains in the provided RegistryChanges
func getNestedChainsFromSteps(chains []api.TestStep, fullRegistry registry) []string {
	// make a set of chain names to prevent duplicates
	nestedChains := sets.NewString()
	for _, change := range chains {
		steps, ok := fullRegistry.chains[*change.Chain]
		if !ok {
			// handle this
		}
		for _, step := range steps {
			// ignore steps that aren't chains
			if step.Chain == nil {
				continue
			}
			// add nested chain to set
			nestedChains.Insert(*step.Chain)
			// recursively check for nested chains and add those to the nested chain set
			for _, chain := range getNestedChainsFromSteps(fullRegistry.chains[*step.Chain], fullRegistry) {
				nestedChains.Insert(chain)
			}
		}
	}
	// convert set into slice
	var nestedChainsSlice []string
	for k := range nestedChains {
		nestedChainsSlice = append(nestedChainsSlice, k)
	}
	return nestedChainsSlice
}

// getNestedChains returns the names of all chains nested inside other chains in the provided RegistryChanges
func getNestedChains(chains []api.RegistryChain, fullRegistry registry) []string {
	// make a set of chain names to prevent duplicates
	nestedChains := sets.NewString()
	for _, change := range chains {
		steps := change.Steps
		for _, step := range steps {
			// ignore steps that aren't chains
			if step.Chain == nil {
				continue
			}
			// add nested chain to set
			nestedChains.Insert(*step.Chain)
			// recursively check for nested chains and add those to the nested chain set
			for _, chain := range getNestedChainsFromSteps(fullRegistry.chains[*step.Chain], fullRegistry) {
				nestedChains.Insert(chain)
			}
		}
	}
	// convert set into slice
	var nestedChainsSlice []string
	for k := range nestedChains {
		nestedChainsSlice = append(nestedChainsSlice, k)
	}
	return nestedChainsSlice
}

func getReferencesInChains(steps []api.TestStep, fullRegistry registry) []string {
	var usedReferences []string
	for _, step := range steps {
		if step.Chain != nil {
			// this should never occur since we always provide unrolledChains; maybe throw an error (otherwise remove this if block)?
			continue
		}
		if step.Reference != nil {
			usedReferences = append(usedReferences, *step.Reference)
		}
	}
	return usedReferences
}

func getStepsInWorkflowChanges(changes []RegistryStep) (refs, chains []string) {
	for _, change := range changes {
		if change.Workflow != nil {
			steps := change.Workflow.Steps
			combinedSteps := append(steps.Pre, steps.Test...)
			combinedSteps = append(combinedSteps, steps.Post...)
			for _, step := range combinedSteps {
				if step.Chain != nil {
					chains = append(chains, *step.Chain)
				} else if step.Reference != nil {
					refs = append(refs, *step.Reference)
				}
			}
		}
	}
	return
}

// trimRegistryChanges removes changed registry components that are included as part of
// other changed components, e.g. a changed reference that is part of a changed chain.
// The trimming order is:
// 1. Remove chains that exist in changed workflows from changed chains
// 2. Remove any chains that are nested inside another changed chain (or chains inside changed workflows)
// 3. Unroll all remaining chains and identify all used references
// 4. Find all references used in workflows
// 5. Remove all references that are included as part of a changed chain or workflow
func trimRegistryChanges(changes []RegistryStep, fullRegistry registry) []RegistryStep {
	wfRefs, wfChains := getStepsInWorkflowChanges(changes)
	var chains []api.RegistryChain
	for _, change := range changes {
		if change.Chain != nil {
			// only get chains not included in changed workflows
			ignore := false
			for _, chain := range wfChains {
				if change.Chain.As == chain {
					ignore = true
				}
			}
			if !ignore {
				chains = append(chains, *change.Chain)
			}
		}
	}
	// for getting nested chains, use both changed chains and chains from changed workflows
	var resolvedWFChains []api.RegistryChain
	for _, chain := range wfChains {
		if resolvedChain, ok := fullRegistry.chains[chain]; !ok {
			// throw error
		} else {
			// put into RegistryChain struct
			registryChain := api.RegistryChain{As: chain, Steps: resolvedChain}
			resolvedWFChains = append(resolvedWFChains, registryChain)
		}
	}
	combinedChains := append(chains, resolvedWFChains...)
	nestedChains := getNestedChains(combinedChains, fullRegistry)
	var trimmedChanges []RegistryStep
	// we still want a []api.TestStep of non-nested chains for later steps
	chains = []api.RegistryChain{}
	for _, change := range changes {
		if change.Chain != nil {
			ignore := false
			for _, nested := range nestedChains {
				if change.Chain.As == nested {
					ignore = true
					break
				}
			}
			if !ignore {
				trimmedChanges = append(trimmedChanges, change)
				chains = append(chains, *change.Chain)
			}
			continue
		}
		trimmedChanges = append(trimmedChanges, change)
	}
	// new combinedChains only contains workflow chains and non-nested chains
	combinedChains = append(chains, resolvedWFChains...)
	// convert from RegistryChain into TestSteps for unrolling
	var chainSteps []api.TestStep
	for _, chain := range chains {
		steps := chain.Steps
		chainSteps = append(chainSteps, steps...)
	}
	unrolledChainSteps, errs := registryutil.UnrollChains(chainSteps, fullRegistry.chains)
	if len(errs) != 0 {
		// handle this
	}
	// get references that exist in changed chains
	var usedReferences []string
	for _, step := range unrolledChainSteps {
		if step.Reference != nil {
			usedReferences = append(usedReferences, *step.Reference)
		}
	}
	usedReferences = append(usedReferences, wfRefs...)
	// remove references that are used in changed chains and workflows
	var trimmedChanges2 []RegistryStep
	for _, change := range trimmedChanges {
		if change.Reference != nil {
			ignore := false
			for _, used := range usedReferences {
				if change.Reference.As == used {
					ignore = true
					break
				}
			}
			if !ignore {
				trimmedChanges2 = append(trimmedChanges, change)
			}
			continue
		}
		trimmedChanges2 = append(trimmedChanges, change)
	}
	return trimmedChanges2
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
