package main

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/afero"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/git/types"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/shardprowconfig"
)

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
							Policy: config.Policy{Protect: utilpointer.Bool(false)},
							Repos: map[string]config.Repo{
								"release": {Policy: config.Policy{Protect: utilpointer.Bool(false)}},
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
								"release": {Policy: config.Policy{Protect: utilpointer.Bool(false)}},
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
					TideGitHubConfig: config.TideGitHubConfig{MergeType: map[string]config.TideOrgMergeType{
						"openshift":         {MergeType: types.MergeSquash},
						"openshift/release": {MergeType: types.MergeRebase},
					}},
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
							Policy: config.Policy{Protect: utilpointer.Bool(false)},
							Repos: map[string]config.Repo{
								"release": {Policy: config.Policy{Protect: utilpointer.Bool(false)}},
							},
						},
					},
				},
				Tide: config.Tide{
					TideGitHubConfig: config.TideGitHubConfig{MergeType: map[string]config.TideOrgMergeType{
						"openshift":         {MergeType: types.MergeSquash},
						"openshift/release": {MergeType: types.MergeRebase},
					}},
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
		{
			name: "Tide queries get sharded",
			in: &config.ProwConfig{Tide: config.Tide{TideGitHubConfig: config.TideGitHubConfig{Queries: config.TideQueries{
				{Orgs: []string{"openshift", "openshift-priv"}, Repos: []string{"kube-reporting/ghostunnel", "kube-reporting/presto"}, Labels: []string{"lgtm", "approved", "bugzilla/valid-bug"}},
				{Orgs: []string{"codeready-toolchain", "integr8ly"}, Repos: []string{"containers/buildah", "containers/common"}, Labels: []string{"lgtm", "approved"}},
				{Orgs: []string{"integr8ly"}, Author: "openshift-bot"},
				{Repos: []string{"openshift/release"}, Author: "openshift-bot"},
			}}}},
			expectedShardFiles: map[string]string{
				"codeready-toolchain/_prowconfig.yaml": `tide:
  queries:
  - labels:
    - lgtm
    - approved
    orgs:
    - codeready-toolchain
`,
				"containers/buildah/_prowconfig.yaml": `tide:
  queries:
  - labels:
    - lgtm
    - approved
    repos:
    - containers/buildah
`,
				"containers/common/_prowconfig.yaml": `tide:
  queries:
  - labels:
    - lgtm
    - approved
    repos:
    - containers/common
`,
				"integr8ly/_prowconfig.yaml": `tide:
  queries:
  - labels:
    - lgtm
    - approved
    orgs:
    - integr8ly
  - author: openshift-bot
    orgs:
    - integr8ly
`,
				"kube-reporting/ghostunnel/_prowconfig.yaml": `tide:
  queries:
  - labels:
    - lgtm
    - approved
    - bugzilla/valid-bug
    repos:
    - kube-reporting/ghostunnel
`,
				"kube-reporting/presto/_prowconfig.yaml": `tide:
  queries:
  - labels:
    - lgtm
    - approved
    - bugzilla/valid-bug
    repos:
    - kube-reporting/presto
`,
				"openshift-priv/_prowconfig.yaml": `tide:
  queries:
  - labels:
    - lgtm
    - approved
    - bugzilla/valid-bug
    orgs:
    - openshift-priv
`,
				"openshift/_prowconfig.yaml": `tide:
  queries:
  - labels:
    - lgtm
    - approved
    - bugzilla/valid-bug
    orgs:
    - openshift
`,
				"openshift/release/_prowconfig.yaml": `tide:
  queries:
  - author: openshift-bot
    repos:
    - openshift/release
`,
			},
		},
		{
			name: "Org and repo slack-reporter configs get written out",
			in: &config.ProwConfig{
				SlackReporterConfigs: map[string](config.SlackReporter){
					"*": {
						SlackReporterConfig: prowjobv1.SlackReporterConfig{
							Channel:           "general-channel",
							JobStatesToReport: []prowjobv1.ProwJobState{"error"},
							ReportTemplate:    "Job {{.Spec.Job}} of type ended with an error",
						},
					},
					"openshift": {
						SlackReporterConfig: prowjobv1.SlackReporterConfig{
							Channel:           "openshift-channel",
							JobStatesToReport: []prowjobv1.ProwJobState{"error"},
							ReportTemplate:    "Job {{.Spec.Job}} of type ended with an error",
						},
					},
					"openshift/installer": {
						SlackReporterConfig: prowjobv1.SlackReporterConfig{
							Channel:           "installer-channel",
							JobStatesToReport: []prowjobv1.ProwJobState{"failure", "error"},
							ReportTemplate:    "Job {{.Spec.Job}} of type ended with state {{.Status.State}}",
						},
					},
				},
			},

			expectedConfig: config.ProwConfig{
				SlackReporterConfigs: map[string](config.SlackReporter){
					"*": {
						SlackReporterConfig: prowjobv1.SlackReporterConfig{
							Channel:           "general-channel",
							JobStatesToReport: []prowjobv1.ProwJobState{"error"},
							ReportTemplate:    "Job {{.Spec.Job}} of type ended with an error",
						},
					},
				},
			},

			expectedShardFiles: map[string]string{
				"openshift/_prowconfig.yaml": strings.Join([]string{
					"slack_reporter_configs:",
					"  openshift:",
					"    channel: openshift-channel",
					"    job_states_to_report:",
					"    - error",
					"    report_template: Job {{.Spec.Job}} of type ended with an error",
					"",
				}, "\n"),
				"openshift/installer/_prowconfig.yaml": strings.Join([]string{
					"slack_reporter_configs:",
					"  openshift/installer:",
					"    channel: installer-channel",
					"    job_states_to_report:",
					"    - failure",
					"    - error",
					"    report_template: Job {{.Spec.Job}} of type ended with state {{.Status.State}}",
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

			newConfig, err := shardprowconfig.ShardProwConfig(tc.in, afs, determinizeProwConfigFunctors{})
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
			if err := os.WriteFile(filepath.Join(tempDir, "_old_config.yaml"), serializedOriginalConfig, 0644); err != nil {
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
			if err := os.WriteFile(filepath.Join(tempDir, "_config.yaml"), serializedNewConfig, 0644); err != nil {
				t.Fatalf("failed to write new config: %v", err)
			}

			for name, content := range shardedConfigFiles {
				path := filepath.Join(tempDir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatalf("failed to create directories for %s: %v", path, err)
				}
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
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
