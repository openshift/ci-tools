package main

import (
	"reflect"
	"sync"
	"testing"

	"sigs.k8s.io/prow/pkg/config"
)

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

func composeProtectedPresubmit(name string) config.Presubmit {
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

func composeRequiredPresubmit(name string) config.Presubmit {
	return config.Presubmit{
		JobBase:   config.JobBase{Name: name},
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
		expected     presubmitTests
	}{
		{
			name: "Org policy requires manual trigger, repo policy does not",
			configGetter: func() *config.Config {
				cfs := config.Config{
					JobConfig: config.JobConfig{PresubmitsStatic: map[string][]config.Presubmit{
						"org/repo": {
							composeProtectedPresubmit("ps1"),
							composeRequiredPresubmit("ps2"),
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
			expected: presubmitTests{
				protected:             []string{"ps1"},
				alwaysRequired:        []string{"ps2"},
				conditionallyRequired: []string{"ps3"},
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
							composeRequiredPresubmit("ps2"),
							{JobBase: config.JobBase{Name: "ps3"}, Optional: true},
						},
					}},
					ProwConfig: decorateWithRepoPolicy(decorateWithOrgPolicy(composeBPConfig())),
				}
			},
			expected: presubmitTests{protected: []string{"ps1"}, alwaysRequired: []string{"ps2"}},
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
			expected: presubmitTests{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &ConfigDataProvider{
				configGetter:      tc.configGetter,
				updatedPresubmits: make(map[string]presubmitTests),
				m:                 sync.Mutex{},
			}
			c.gatherData()
			actual := c.GetPresubmits("org/repo")
			if !reflect.DeepEqual(actual, tc.expected) {
				t.Errorf("expected %v, got %v", tc.expected, actual)
			}
		})
	}
}
