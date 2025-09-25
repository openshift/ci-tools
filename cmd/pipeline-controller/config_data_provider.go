package main

import (
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
)

// RepoLister is a function that returns a list of "org/repo" strings that should be processed
type RepoLister func() []string

type presubmitTests struct {
	protected                     []string
	alwaysRequired                []string
	conditionallyRequired         []string
	pipelineConditionallyRequired []config.Presubmit
	pipelineSkipOnlyRequired      []config.Presubmit
}

type ConfigDataProvider struct {
	configGetter      config.Getter
	repoLister        RepoLister
	updatedPresubmits map[string]presubmitTests
	previousRepoList  []string // Store previous repo list for comparison
	logger            *logrus.Entry
	m                 sync.Mutex
}

func NewConfigDataProvider(configGetter config.Getter, repoLister RepoLister, logger *logrus.Entry) *ConfigDataProvider {
	provider := &ConfigDataProvider{
		configGetter:      configGetter,
		repoLister:        repoLister,
		updatedPresubmits: make(map[string]presubmitTests),
		previousRepoList:  []string{},
		logger:            logger,
		m:                 sync.Mutex{},
	}
	// Initialize with first load
	provider.gatherDataWithChangeDetection()
	return provider
}

func (c *ConfigDataProvider) GetPresubmits(orgRepo string) presubmitTests {
	c.m.Lock()
	defer c.m.Unlock()
	if presubmits, ok := c.updatedPresubmits[orgRepo]; ok {
		return presubmits
	}
	return presubmitTests{}
}

func (c *ConfigDataProvider) Run() {
	for {
		time.Sleep(10 * time.Minute)
		c.gatherDataWithChangeDetection()
	}
}

func (c *ConfigDataProvider) gatherData() {
	// Get the list of repositories from the pipeline controller configs (config + lgtm config)
	orgRepos := c.repoLister()
	c.gatherDataForRepos(orgRepos)
}

// gatherDataForRepos processes the given list of repositories and updates the presubmits data
func (c *ConfigDataProvider) gatherDataForRepos(orgRepos []string) {
	cfg := c.configGetter()

	updatedPresubmits := make(map[string]presubmitTests)
	for _, orgRepo := range orgRepos {
		presubmits := cfg.GetPresubmitsStatic(orgRepo)
		for _, p := range presubmits {
			if !p.AlwaysRun && p.RunIfChanged == "" && p.SkipIfOnlyChanged == "" {
				if val, ok := p.Annotations["pipeline_run_if_changed"]; ok && val != "" {
					pre := updatedPresubmits[orgRepo]
					pre.pipelineConditionallyRequired = append(pre.pipelineConditionallyRequired, p)
					updatedPresubmits[orgRepo] = pre
					continue
				}
				// Also check for pipeline_skip_only_if_changed annotation
				if val, ok := p.Annotations["pipeline_skip_only_if_changed"]; ok && val != "" {
					pre := updatedPresubmits[orgRepo]
					pre.pipelineSkipOnlyRequired = append(pre.pipelineSkipOnlyRequired, p)
					updatedPresubmits[orgRepo] = pre
					continue
				}
				// Only categorize as protected if it doesn't have pipeline annotations
				if !p.Optional {
					if _, hasPipelineRun := p.Annotations["pipeline_run_if_changed"]; !hasPipelineRun {
						if _, hasPipelineSkip := p.Annotations["pipeline_skip_only_if_changed"]; !hasPipelineSkip {
							pre := updatedPresubmits[orgRepo]
							pre.protected = append(pre.protected, p.Name)
							updatedPresubmits[orgRepo] = pre
						}
					}
				}
			}
			if !p.Optional && p.AlwaysRun {
				pre := updatedPresubmits[orgRepo]
				pre.alwaysRequired = append(pre.alwaysRequired, p.Name)
				updatedPresubmits[orgRepo] = pre
				continue
			}
			if !p.Optional && (p.RunIfChanged != "" || p.SkipIfOnlyChanged != "") {
				pre := updatedPresubmits[orgRepo]
				pre.conditionallyRequired = append(pre.conditionallyRequired, p.Name)
				updatedPresubmits[orgRepo] = pre
				continue
			}
		}
	}
	c.m.Lock()
	defer c.m.Unlock()
	c.updatedPresubmits = updatedPresubmits
}

// gatherDataWithChangeDetection checks if the repository list has changed and only reloads if needed
func (c *ConfigDataProvider) gatherDataWithChangeDetection() {
	// Get current repo list
	currentRepoList := c.repoLister()

	// Compare with previous repo list
	c.m.Lock()
	previousCount := len(c.previousRepoList)
	hasChanged := !c.repoListsEqual(c.previousRepoList, currentRepoList)
	if hasChanged {
		// Store the new repo list
		c.previousRepoList = make([]string, len(currentRepoList))
		copy(c.previousRepoList, currentRepoList)
	}
	c.m.Unlock()

	if hasChanged {
		c.logger.WithFields(logrus.Fields{
			"previous_count": previousCount,
			"current_count":  len(currentRepoList),
		}).Info("Repository configuration change detected, reloading pipeline data")
		c.gatherDataForRepos(currentRepoList)
	} else {
		c.logger.Debug("No repository configuration changes detected, skipping reload")
	}
}

// repoListsEqual compares two repository lists for equality (order-independent)
func (c *ConfigDataProvider) repoListsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	// Create sorted copies for comparison
	sortedA := make([]string, len(a))
	sortedB := make([]string, len(b))
	copy(sortedA, a)
	copy(sortedB, b)

	sort.Strings(sortedA)
	sort.Strings(sortedB)

	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}

	return true
}
