package main

import (
	"flag"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	fuzz "github.com/google/gofuzz"
	"github.com/spf13/afero"

	"k8s.io/test-infra/prow/config"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/github"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/yaml"
)

// TestDeduplicateTideQueriesDoesntLoseData simply uses deduplicateTideQueries
// on a single fuzzed tidequery, which should never result in any change as
// there is nothing that could be deduplicated. This is mostly to ensure we
// don't forget to change our code when new fields get added to the type.
func TestDeduplicateTideQueriesDoesntLoseData(t *testing.T) {
	for i := 0; i < 100; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			query := config.TideQuery{}
			fuzz.New().Fuzz(&query)
			result, err := deduplicateTideQueries(config.TideQueries{query})
			if err != nil {
				t.Fatalf("error: %v", err)
			}

			if diff := cmp.Diff(result[0], query); diff != "" {
				t.Errorf("result differs from initial query: %s", diff)
			}
		})
	}
}

func TestDeduplicateTideQueries(t *testing.T) {
	testCases := []struct {
		name     string
		in       config.TideQueries
		expected config.TideQueries
	}{
		{
			name: "No overlap",
			in: config.TideQueries{
				{Orgs: []string{"openshift"}, Labels: []string{"merge-me"}},
				{Orgs: []string{"openshift-priv"}, Labels: []string{"merge-me-differently"}},
			},
			expected: config.TideQueries{
				{Orgs: []string{"openshift"}, Labels: []string{"merge-me"}},
				{Orgs: []string{"openshift-priv"}, Labels: []string{"merge-me-differently"}},
			},
		},
		{
			name: "Queries get deduplicated",
			in: config.TideQueries{
				{Orgs: []string{"openshift"}, Labels: []string{"merge-me"}},
				{Orgs: []string{"openshift-priv"}, Labels: []string{"merge-me"}},
			},
			expected: config.TideQueries{{Orgs: []string{"openshift", "openshift-priv"}, Labels: []string{"merge-me"}}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := deduplicateTideQueries(tc.in)
			if err != nil {
				t.Fatalf("failed: %v", err)
			}
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Errorf("Result differs from expected: %v", diff)
			}
		})
	}
}

func TestShardProwConfig(t *testing.T) {
	testCases := []struct {
		name string
		in   *config.ProwConfig

		expectedConfig     config.ProwConfig
		expectedShardFiles map[string]string
	}{
		{
			name: "Org and repo branchprotection config get written out",
			in: &config.ProwConfig{
				BranchProtection: config.BranchProtection{
					Orgs: map[string]config.Org{
						"openshift": {
							Policy: config.Policy{Protect: utilpointer.BoolPtr(false)},
							Repos: map[string]config.Repo{
								"release": {Policy: config.Policy{Protect: utilpointer.BoolPtr(false)}},
							},
						},
					},
				},
			},

			expectedShardFiles: map[string]string{
				"openshift/_prowconfig.yaml": strings.Join([]string{
					"branch-protection:",
					"  orgs:",
					"    openshift:",
					"      protect: false",
					"",
				}, "\n"),
				"openshift/release/_prowconfig.yaml": strings.Join([]string{
					"branch-protection:",
					"  orgs:",
					"    openshift:",
					"      repos:",
					"        release:",
					"          protect: false",
					"",
				}, "\n"),
			},
		},
		{
			name: "Empty org branchprotection config is not serialized",
			in: &config.ProwConfig{
				BranchProtection: config.BranchProtection{
					Orgs: map[string]config.Org{
						"openshift": {
							Repos: map[string]config.Repo{
								"release": {Policy: config.Policy{Protect: utilpointer.BoolPtr(false)}},
							},
						},
					},
				},
			},

			expectedShardFiles: map[string]string{
				"openshift/release/_prowconfig.yaml": strings.Join([]string{
					"branch-protection:",
					"  orgs:",
					"    openshift:",
					"      repos:",
					"        release:",
					"          protect: false",
					"",
				}, "\n"),
			},
		},
		{
			name: "Org and repo mergemethod config gets written out",
			in: &config.ProwConfig{
				Tide: config.Tide{
					MergeType: map[string]github.PullRequestMergeType{
						"openshift":         github.MergeSquash,
						"openshift/release": github.MergeRebase,
					},
				},
			},

			expectedShardFiles: map[string]string{
				"openshift/_prowconfig.yaml": strings.Join([]string{
					"tide:",
					"  merge_method:",
					"    openshift: squash",
					"",
				}, "\n"),
				"openshift/release/_prowconfig.yaml": strings.Join([]string{
					"tide:",
					"  merge_method:",
					"    openshift/release: rebase",
					"",
				}, "\n"),
			},
		},
		{
			name: "Org and repo branchprotection and mergemethod config gets written out",
			in: &config.ProwConfig{
				BranchProtection: config.BranchProtection{
					Orgs: map[string]config.Org{
						"openshift": {
							Policy: config.Policy{Protect: utilpointer.BoolPtr(false)},
							Repos: map[string]config.Repo{
								"release": {Policy: config.Policy{Protect: utilpointer.BoolPtr(false)}},
							},
						},
					},
				},
				Tide: config.Tide{
					MergeType: map[string]github.PullRequestMergeType{
						"openshift":         github.MergeSquash,
						"openshift/release": github.MergeRebase,
					},
				},
			},

			expectedShardFiles: map[string]string{
				"openshift/_prowconfig.yaml": strings.Join([]string{
					"branch-protection:",
					"  orgs:",
					"    openshift:",
					"      protect: false",
					"tide:",
					"  merge_method:",
					"    openshift: squash",
					"",
				}, "\n"),
				"openshift/release/_prowconfig.yaml": strings.Join([]string{
					"branch-protection:",
					"  orgs:",
					"    openshift:",
					"      repos:",
					"        release:",
					"          protect: false",
					"tide:",
					"  merge_method:",
					"    openshift/release: rebase",
					"",
				}, "\n"),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			afs := afero.NewMemMapFs()
			serializedOriginalConfig, err := yaml.Marshal(tc.in)
			if err != nil {
				t.Fatalf("failed to serialize the original config: %v", err)
			}

			newConfig, err := shardProwConfig(tc.in, afs)
			if err != nil {
				t.Fatalf("shardProwConfig failed: %v", err)
			}
			if diff := cmp.Diff(&tc.expectedConfig, newConfig, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("config with extracted shards differs from expected: %s", diff)
			}

			shardedConfigFiles := map[string]string{}
			if err := afero.Walk(afs, "", func(path string, info fs.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return err
				}
				if filepath.Base(path) != "_prowconfig.yaml" {
					t.Errorf("found file %s which doesn't have the expected _prowconfig.yaml name", path)
				}
				data, err := afero.ReadFile(afs, path)
				if err != nil {
					t.Errorf("failed to read file %s: %v", path, err)
				}
				shardedConfigFiles[path] = string(data)
				return nil
			}); err != nil {
				t.Errorf("waking the fs failed: %v", err)
			}

			if diff := cmp.Diff(tc.expectedShardFiles, shardedConfigFiles); diff != "" {
				t.Fatalf("actual sharded config differs from expected:\n%s", diff)
			}

			// Write both the old and the new config, then load it, then serialize, then compare.
			// This is more of test for the merging, but an important safety check.
			// We need to do the annoying dance to get two defaulted configs that are comparable.
			tempDir := t.TempDir()
			if err := ioutil.WriteFile(filepath.Join(tempDir, "_old_config.yaml"), serializedOriginalConfig, 0644); err != nil {
				t.Fatalf("failed to write old config: %v", err)
			}
			oldConfigDefaulted, err := config.Load(filepath.Join(tempDir, "_old_config.yaml"), "", nil, "")
			if err != nil {
				t.Fatalf("failed to load the old config: %v", err)
			}
			oldCOnfigDefaultedAndSerialized, err := yaml.Marshal(oldConfigDefaulted)
			if err != nil {
				t.Fatalf("failed to serialize old config after writing and reading it: %v", err)
			}

			serializedNewConfig, err := yaml.Marshal(newConfig)
			if err != nil {
				t.Fatalf("failed to marshal the new config: %v", err)
			}
			if err := ioutil.WriteFile(filepath.Join(tempDir, "_config.yaml"), serializedNewConfig, 0644); err != nil {
				t.Fatalf("failed to write new config: %v", err)
			}

			for name, content := range shardedConfigFiles {
				path := filepath.Join(tempDir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatalf("failed to create directories for %s: %v", path, err)
				}
				if err := ioutil.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatalf("failed to write file %s: %v", name, err)
				}
			}

			fs := &flag.FlagSet{}
			configOpts := configflagutil.ConfigOptions{}
			configOpts.AddFlags(fs)
			if err := fs.Parse([]string{
				"--config-path=" + filepath.Join(tempDir, "_config.yaml"),
				"--supplemental-prow-config-dir=" + tempDir,
			}); err != nil {
				t.Fatalf("faield to parse flags")
			}
			agent, err := configOpts.ConfigAgent()
			if err != nil {
				t.Fatalf("failed to get config agent: %v", err)
			}
			serializedNewMergedConfig, err := yaml.Marshal(agent.Config())
			if err != nil {
				t.Fatalf("failed to serialize config after merging: %v", err)
			}

			if diff := cmp.Diff(oldCOnfigDefaultedAndSerialized, serializedNewMergedConfig); diff != "" {
				t.Errorf("after re-reading sharded config, it differs from what we originally had: %s", diff)
			}
		})
	}
}
