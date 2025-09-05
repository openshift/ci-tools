package main

import (
	"os"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestConfigBackwardsCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		yamlContent   string
		expectedOrgs  int
		expectedRepos map[string]map[string]string // org -> repo -> trigger
	}{
		{
			name: "old format with string repos",
			yamlContent: `orgs:
  - org: openshift
    repos:
      - cluster-capi-operator
      - installer
`,
			expectedOrgs: 1,
			expectedRepos: map[string]map[string]string{
				"openshift": {
					"cluster-capi-operator": "auto",
					"installer":             "auto",
				},
			},
		},
		{
			name: "new format with object repos",
			yamlContent: `orgs:
  - org: openshift
    repos:
      - name: cluster-capi-operator
        mode:
          trigger: manual
      - name: installer
        mode:
          trigger: auto
`,
			expectedOrgs: 1,
			expectedRepos: map[string]map[string]string{
				"openshift": {
					"cluster-capi-operator": "manual",
					"installer":             "auto",
				},
			},
		},
		{
			name: "new format with missing trigger (should default to auto)",
			yamlContent: `orgs:
  - org: openshift
    repos:
      - name: cluster-capi-operator
`,
			expectedOrgs: 1,
			expectedRepos: map[string]map[string]string{
				"openshift": {
					"cluster-capi-operator": "auto",
				},
			},
		},
		{
			name: "mixed format (not recommended but should work)",
			yamlContent: `orgs:
  - org: openshift
    repos:
      - installer
      - name: cluster-capi-operator
        mode:
          trigger: manual
`,
			expectedOrgs: 1,
			expectedRepos: map[string]map[string]string{
				"openshift": {
					"installer":             "auto",
					"cluster-capi-operator": "manual",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create temporary file with YAML content
			tmpfile, err := os.CreateTemp("", "config-*.yaml")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(tc.yamlContent)); err != nil {
				t.Fatal(err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatal(err)
			}

			// Parse the config
			var config enabledConfig
			yamlFile, err := os.Open(tmpfile.Name())
			if err != nil {
				t.Fatal(err)
			}
			defer yamlFile.Close()

			decoder := yaml.NewDecoder(yamlFile)
			if err := decoder.Decode(&config); err != nil {
				t.Fatalf("Failed to decode YAML: %v", err)
			}

			// Verify the results
			if len(config.Orgs) != tc.expectedOrgs {
				t.Errorf("Expected %d orgs, got %d", tc.expectedOrgs, len(config.Orgs))
			}

			for _, org := range config.Orgs {
				expectedRepos, ok := tc.expectedRepos[org.Org]
				if !ok {
					t.Errorf("Unexpected org: %s", org.Org)
					continue
				}

				if len(org.Repos) != len(expectedRepos) {
					t.Errorf("For org %s, expected %d repos, got %d", org.Org, len(expectedRepos), len(org.Repos))
				}

				for _, repo := range org.Repos {
					expectedTrigger, ok := expectedRepos[repo.Name]
					if !ok {
						t.Errorf("Unexpected repo %s in org %s", repo.Name, org.Org)
						continue
					}

					if repo.Mode.Trigger != expectedTrigger {
						t.Errorf("For repo %s/%s, expected trigger %s, got %s", org.Org, repo.Name, expectedTrigger, repo.Mode.Trigger)
					}
				}
			}
		})
	}
}

func TestWatcherGetConfig(t *testing.T) {
	// Test that the watcher.getConfig() method properly converts the config
	w := &watcher{
		config: enabledConfig{
			Orgs: []struct {
				Org   string     `yaml:"org"`
				Repos []RepoItem `yaml:"repos"`
			}{
				{
					Org: "openshift",
					Repos: []RepoItem{
						{Name: "repo1", Mode: struct{ Trigger string }{Trigger: "auto"}},
						{Name: "repo2", Mode: struct{ Trigger string }{Trigger: "manual"}},
					},
				},
			},
		},
	}

	config := w.getConfig()

	// Verify the results
	if len(config) != 1 {
		t.Errorf("Expected 1 org, got %d", len(config))
	}

	repos, ok := config["openshift"]
	if !ok {
		t.Error("Expected org 'openshift' not found")
	}

	if len(repos) != 2 {
		t.Errorf("Expected 2 repos, got %d", len(repos))
	}

	if repos["repo1"].Trigger != "auto" {
		t.Errorf("Expected repo1 trigger to be 'auto', got %s", repos["repo1"].Trigger)
	}

	if repos["repo2"].Trigger != "manual" {
		t.Errorf("Expected repo2 trigger to be 'manual', got %s", repos["repo2"].Trigger)
	}
}
