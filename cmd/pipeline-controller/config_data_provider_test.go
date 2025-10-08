package main

import (
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
)

// testLogger creates a discarded logger for tests
func testLogger() *logrus.Entry {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return logrus.NewEntry(logger)
}

func yes() *bool {
	yes := true
	return &yes
}

func composeBPConfig() config.ProwConfig {
	return config.ProwConfig{
		BranchProtection: config.BranchProtection{
			Orgs: map[string]config.Org{
				"org": {
					Policy: config.Policy{},
					Repos: map[string]config.Repo{
						"repo": {
							Policy:   config.Policy{},
							Branches: make(map[string]config.Branch),
						},
					}},
			},
		},
	}
}

func decorateWithOrgPolicy(cfg config.ProwConfig) config.ProwConfig {
	if org, ok := cfg.BranchProtection.Orgs["org"]; ok {
		org.Policy.RequireManuallyTriggeredJobs = yes()
		cfg.BranchProtection.Orgs["org"] = org

	}
	return cfg
}

func decorateWithRepoPolicy(cfg config.ProwConfig) config.ProwConfig {
	if org, ok := cfg.BranchProtection.Orgs["org"]; ok {
		if repo, ok := org.Repos["repo"]; ok {
			repo.Policy.RequireManuallyTriggeredJobs = yes()
			cfg.BranchProtection.Orgs["org"].Repos["repo"] = repo
		}
	}
	return cfg
}

func composeProtectedPresubmit(name string) config.Presubmit { //nolint:unparam // parameter allows flexibility for future test cases
	return config.Presubmit{
		JobBase:   config.JobBase{Name: name},
		AlwaysRun: false,
		Optional:  false,
		RegexpChangeMatcher: config.RegexpChangeMatcher{
			SkipIfOnlyChanged: "",
			RunIfChanged:      "",
		},
	}
}

func composeRequiredPresubmit() config.Presubmit {
	return config.Presubmit{
		JobBase:   config.JobBase{Name: "ps2"},
		AlwaysRun: true,
		Optional:  false,
	}
}

func composeCondRequiredPresubmit(name string) config.Presubmit {
	return config.Presubmit{
		JobBase:             config.JobBase{Name: name},
		RegexpChangeMatcher: config.RegexpChangeMatcher{RunIfChanged: ".*"},
	}
}

func composePipelineCondRequiredPresubmit(name string, optional bool, annotations map[string]string) config.Presubmit {
	return config.Presubmit{
		AlwaysRun: false,
		Optional:  optional,
		JobBase:   config.JobBase{Name: name, Annotations: annotations},
	}
}

func TestConfigDataProviderGatherData(t *testing.T) {
	tests := []struct {
		name         string
		configGetter config.Getter
		repoLister   RepoLister
		expected     presubmitTests
	}{
		{
			name: "Org policy requires manual trigger, repo policy does not",
			configGetter: func() *config.Config {
				cfs := config.Config{
					JobConfig: config.JobConfig{PresubmitsStatic: map[string][]config.Presubmit{
						"org/repo": {
							composeProtectedPresubmit("ps1"),
							composeRequiredPresubmit(),
							composeCondRequiredPresubmit("ps3"),
							composePipelineCondRequiredPresubmit("ps4", false, map[string]string{"pipeline_run_if_changed": ".*"}),
							composePipelineCondRequiredPresubmit("ps5", true, map[string]string{"pipeline_run_if_changed": ".*"}),
							composePipelineCondRequiredPresubmit("ps6", true, map[string]string{}),
						},
					}},
					ProwConfig: decorateWithOrgPolicy(composeBPConfig()),
				}
				return &cfs
			},
			repoLister: func() []string {
				return []string{"org/repo"}
			},
			expected: presubmitTests{
				protected:             []config.Presubmit{composeProtectedPresubmit("ps1")},
				alwaysRequired:        []config.Presubmit{composeRequiredPresubmit()},
				conditionallyRequired: []config.Presubmit{composeCondRequiredPresubmit("ps3")},
				pipelineConditionallyRequired: []config.Presubmit{
					composePipelineCondRequiredPresubmit("ps4", false, map[string]string{"pipeline_run_if_changed": ".*"}),
					composePipelineCondRequiredPresubmit("ps5", true, map[string]string{"pipeline_run_if_changed": ".*"}),
				}},
		},
		{
			name: "Org policy and repo require manual trigger",
			configGetter: func() *config.Config {
				return &config.Config{
					JobConfig: config.JobConfig{PresubmitsStatic: map[string][]config.Presubmit{
						"org/repo": {
							composeProtectedPresubmit("ps1"),
							composeRequiredPresubmit(),
							{JobBase: config.JobBase{Name: "ps3"}, Optional: true},
						},
					}},
					ProwConfig: decorateWithRepoPolicy(decorateWithOrgPolicy(composeBPConfig())),
				}
			},
			repoLister: func() []string {
				return []string{"org/repo"}
			},
			expected: presubmitTests{protected: []config.Presubmit{composeProtectedPresubmit("ps1")}, alwaysRequired: []config.Presubmit{composeRequiredPresubmit()}},
		},
		{
			name: "No manual trigger required",
			configGetter: func() *config.Config {
				return &config.Config{
					JobConfig: config.JobConfig{PresubmitsStatic: map[string][]config.Presubmit{
						"org/repo": {
							composeProtectedPresubmit("ps1"),
						},
					}},
					ProwConfig: composeBPConfig(),
				}
			},
			repoLister: func() []string {
				return []string{} // No repos configured - this should result in empty results
			},
			expected: presubmitTests{},
		},
		{
			name: "Jobs with pipeline_skip_if_only_changed are collected",
			configGetter: func() *config.Config {
				cfs := config.Config{
					JobConfig: config.JobConfig{PresubmitsStatic: map[string][]config.Presubmit{
						"org/repo": {
							composeProtectedPresubmit("ps1"),
							composeRequiredPresubmit(),
							composePipelineCondRequiredPresubmit("ps3", false, map[string]string{"pipeline_skip_if_only_changed": "^docs/.*"}),
							composePipelineCondRequiredPresubmit("ps4", true, map[string]string{"pipeline_skip_if_only_changed": "^test/.*"}),
							composePipelineCondRequiredPresubmit("ps5", false, map[string]string{"pipeline_run_if_changed": `.*\.go`}),
						},
					}},
					ProwConfig: decorateWithOrgPolicy(composeBPConfig()),
				}
				return &cfs
			},
			repoLister: func() []string {
				return []string{"org/repo"}
			},
			expected: presubmitTests{
				protected:             []config.Presubmit{composeProtectedPresubmit("ps1"), composeProtectedPresubmit("ps3")},
				alwaysRequired:        []config.Presubmit{composeRequiredPresubmit()},
				conditionallyRequired: []config.Presubmit{},
				pipelineConditionallyRequired: []config.Presubmit{
					composePipelineCondRequiredPresubmit("ps5", false, map[string]string{"pipeline_run_if_changed": `.*\.go`}),
				},
				pipelineSkipOnlyRequired: []config.Presubmit{},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &ConfigDataProvider{
				configGetter:      tc.configGetter,
				repoLister:        tc.repoLister,
				updatedPresubmits: make(map[string]presubmitTests),
				previousRepoList:  []string{},
				logger:            testLogger(),
				m:                 sync.Mutex{},
			}
			c.gatherData()
			actual := c.GetPresubmits("org/repo")
			// Compare protected presubmits by name
			if len(actual.protected) != len(tc.expected.protected) {
				t.Errorf("protected length - expected %d, got %d", len(tc.expected.protected), len(actual.protected))
			} else {
				for _, expected := range tc.expected.protected {
					found := false
					for _, actualItem := range actual.protected {
						if expected.Name == actualItem.Name {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("protected - expected to find job %s", expected.Name)
					}
				}
			}

			// Compare always required presubmits by name
			if len(actual.alwaysRequired) != len(tc.expected.alwaysRequired) {
				t.Errorf("alwaysRequired length - expected %d, got %d", len(tc.expected.alwaysRequired), len(actual.alwaysRequired))
			} else {
				for _, expected := range tc.expected.alwaysRequired {
					found := false
					for _, actualItem := range actual.alwaysRequired {
						if expected.Name == actualItem.Name {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("alwaysRequired - expected to find job %s", expected.Name)
					}
				}
			}

			// Compare conditionally required presubmits by name
			if len(actual.conditionallyRequired) != len(tc.expected.conditionallyRequired) {
				t.Errorf("conditionallyRequired length - expected %d, got %d", len(tc.expected.conditionallyRequired), len(actual.conditionallyRequired))
			} else {
				for _, expected := range tc.expected.conditionallyRequired {
					found := false
					for _, actualItem := range actual.conditionallyRequired {
						if expected.Name == actualItem.Name {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("conditionallyRequired - expected to find job %s", expected.Name)
					}
				}
			}
			// For pipelineConditionallyRequired, check length and then check each item exists
			if len(actual.pipelineConditionallyRequired) != len(tc.expected.pipelineConditionallyRequired) {
				t.Errorf("pipelineConditionallyRequired length - expected %d, got %d",
					len(tc.expected.pipelineConditionallyRequired),
					len(actual.pipelineConditionallyRequired))
				return
			}

			// Check that each expected item exists in actual
			for _, expected := range tc.expected.pipelineConditionallyRequired {
				found := false
				for _, actualItem := range actual.pipelineConditionallyRequired {
					if expected.Name == actualItem.Name &&
						reflect.DeepEqual(expected.Annotations, actualItem.Annotations) &&
						expected.Optional == actualItem.Optional {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("pipelineConditionallyRequired - expected to find job %s with annotations %v and optional=%v",
						expected.Name, expected.Annotations, expected.Optional)
				}
			}
			// For pipelineSkipOnlyRequired, check length and then check each item exists
			if len(actual.pipelineSkipOnlyRequired) != len(tc.expected.pipelineSkipOnlyRequired) {
				t.Errorf("pipelineSkipOnlyRequired length - expected %d, got %d",
					len(tc.expected.pipelineSkipOnlyRequired),
					len(actual.pipelineSkipOnlyRequired))
			} else {
				for _, expectedJob := range tc.expected.pipelineSkipOnlyRequired {
					found := false
					for _, actualJob := range actual.pipelineSkipOnlyRequired {
						if expectedJob.Name == actualJob.Name &&
							reflect.DeepEqual(expectedJob.Annotations, actualJob.Annotations) &&
							expectedJob.Optional == actualJob.Optional {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("pipelineSkipOnlyRequired - expected job %s not found", expectedJob.Name)
					}
				}
			}
		})
	}
}

func TestConfigDataProviderRepoListComparison(t *testing.T) {
	tests := []struct {
		name     string
		listA    []string
		listB    []string
		expected bool
	}{
		{
			name:     "identical lists",
			listA:    []string{"org1/repo1", "org2/repo2"},
			listB:    []string{"org1/repo1", "org2/repo2"},
			expected: true,
		},
		{
			name:     "different order - should be equal",
			listA:    []string{"org1/repo1", "org2/repo2"},
			listB:    []string{"org2/repo2", "org1/repo1"},
			expected: true,
		},
		{
			name:     "different lengths",
			listA:    []string{"org1/repo1", "org2/repo2"},
			listB:    []string{"org1/repo1"},
			expected: false,
		},
		{
			name:     "different contents",
			listA:    []string{"org1/repo1", "org2/repo2"},
			listB:    []string{"org1/repo1", "org3/repo3"},
			expected: false,
		},
		{
			name:     "empty lists",
			listA:    []string{},
			listB:    []string{},
			expected: true,
		},
		{
			name:     "one empty, one not",
			listA:    []string{"org1/repo1"},
			listB:    []string{},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &ConfigDataProvider{
				logger: testLogger(),
			}
			result := c.repoListsEqual(tc.listA, tc.listB)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v for lists %v and %v", tc.expected, result, tc.listA, tc.listB)
			}
		})
	}
}

func TestConfigDataProviderChangeDetection(t *testing.T) {
	// Use a slice to define the sequence of repo lists that will be returned
	repoSequence := [][]string{
		{"org1/repo1", "org2/repo2"},               // First call
		{"org1/repo1", "org2/repo2"},               // Second call (same as first)
		{"org1/repo1", "org2/repo2", "org3/repo3"}, // Third call (added repo)
	}
	callIndex := -1

	c := &ConfigDataProvider{
		configGetter: func() *config.Config {
			return &config.Config{
				JobConfig: config.JobConfig{PresubmitsStatic: map[string][]config.Presubmit{
					"org1/repo1": {composeProtectedPresubmit("test-protected-1")},
					"org2/repo2": {composeProtectedPresubmit("test-protected-2")},
					"org3/repo3": {composeProtectedPresubmit("test-protected-3")},
				}},
			}
		},
		repoLister: func() []string {
			callIndex++
			if callIndex >= len(repoSequence) {
				callIndex = len(repoSequence) - 1 // Stay at last sequence
			}
			result := repoSequence[callIndex]
			t.Logf("RepoLister call %d returning: %v", callIndex, result)
			return result
		},
		updatedPresubmits: make(map[string]presubmitTests),
		previousRepoList:  []string{},
		logger:            testLogger(),
		m:                 sync.Mutex{},
	}

	// First call should always gather data (initialization)
	c.gatherDataWithChangeDetection()
	firstRunLength := len(c.updatedPresubmits)
	t.Logf("First run length: %d (should be 2)", firstRunLength)
	if firstRunLength != 2 {
		t.Errorf("Expected 2 repos on first run, got %d", firstRunLength)
	}

	// Second call with same repo list should NOT reload data
	c.gatherDataWithChangeDetection()
	secondRunLength := len(c.updatedPresubmits)
	t.Logf("Second run length: %d (should be same as first: %d)", secondRunLength, firstRunLength)
	if secondRunLength != firstRunLength {
		t.Errorf("Second run should have same length as first run, but got %d vs %d", secondRunLength, firstRunLength)
	}

	// Third call with different repo list should reload data
	c.gatherDataWithChangeDetection()
	thirdRunLength := len(c.updatedPresubmits)
	t.Logf("Third run length: %d (should be 3)", thirdRunLength)
	if thirdRunLength != 3 {
		t.Errorf("Expected 3 repos after adding org3/repo3, but got %d", thirdRunLength)
	}
}

func TestConfigDataProviderChangeDetectionWithRemovedRepo(t *testing.T) {
	callCount := 0

	c := &ConfigDataProvider{
		configGetter: func() *config.Config {
			return &config.Config{
				JobConfig: config.JobConfig{PresubmitsStatic: map[string][]config.Presubmit{
					"org1/repo1": {composeProtectedPresubmit("test-protected-1")},
					"org2/repo2": {composeProtectedPresubmit("test-protected-2")},
					"org3/repo3": {composeProtectedPresubmit("test-protected-3")},
				}},
			}
		},
		repoLister: func() []string {
			callCount++
			switch callCount {
			case 1, 2:
				return []string{"org1/repo1", "org2/repo2", "org3/repo3"}
			default:
				return []string{"org1/repo1", "org2/repo2"} // Removed org3/repo3
			}
		},
		updatedPresubmits: make(map[string]presubmitTests),
		previousRepoList:  []string{},
		logger:            testLogger(),
		m:                 sync.Mutex{},
	}

	// First call - should gather data (initialization)
	c.gatherDataWithChangeDetection()
	firstRunLength := len(c.updatedPresubmits)
	if firstRunLength == 0 {
		t.Error("Expected updatedPresubmits to be populated on first run")
	}

	// Second call with same list - should not cause a reload
	c.gatherDataWithChangeDetection()
	secondRunLength := len(c.updatedPresubmits)
	if secondRunLength != firstRunLength {
		t.Logf("Second run length (%d) vs first run length (%d)", secondRunLength, firstRunLength)
	}

	// Third call with removed repo - should cause a reload (fewer repos)
	c.gatherDataWithChangeDetection()
	thirdRunLength := len(c.updatedPresubmits)
	if thirdRunLength >= firstRunLength {
		t.Errorf("Expected fewer repos after removal, but length went from %d to %d", firstRunLength, thirdRunLength)
	}
}
