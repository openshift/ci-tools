package prowconfigsharding

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"

	pluginsflagutil "sigs.k8s.io/prow/pkg/flagutil/plugins"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/yaml"
)

func TestShardPluginConfig(t *testing.T) {
	t.Parallel()
	targetRelease46 := "4.6.0"
	targetRelease47 := "4.7.0"
	targetRelease48 := "4.8.0"
	testCases := []struct {
		name string
		in   *plugins.Configuration

		expectedConfig     *plugins.Configuration
		expectedShardFiles map[string]string
	}{
		{
			name: "Plugin config gets sharded",
			in: &plugins.Configuration{
				Approve: []plugins.Approve{
					{
						IssueRequired: true,
						Repos:         []string{"openshift"},
					},
					{
						LgtmActsAsApprove: true,
						Repos:             []string{"openshift/release"},
					},
					{
						LgtmActsAsApprove: true,
						Repos:             []string{"openshift/release2"},
					},
				},
				Plugins: plugins.Plugins{
					"openshift":          plugins.OrgPlugins{Plugins: []string{"foo"}},
					"openshift/release":  plugins.OrgPlugins{Plugins: []string{"bar"}},
					"openshift/release2": plugins.OrgPlugins{Plugins: []string{"zim"}},
				},
				Bugzilla: plugins.Bugzilla{
					Default: map[string]plugins.BugzillaBranchOptions{
						"master": {TargetRelease: &targetRelease48},
					},
					Orgs: map[string]plugins.BugzillaOrgOptions{
						"openshift": {
							Default: map[string]plugins.BugzillaBranchOptions{
								"release-4.6": {TargetRelease: &targetRelease46},
							},
							Repos: map[string]plugins.BugzillaRepoOptions{
								"release": {
									Branches: map[string]plugins.BugzillaBranchOptions{
										"release-4.8": {TargetRelease: &targetRelease48},
									},
								},
								"release3": {
									Branches: map[string]plugins.BugzillaBranchOptions{
										"release-4.7": {TargetRelease: &targetRelease47},
									},
								},
							},
						},
						"openshift-priv": {
							Default: map[string]plugins.BugzillaBranchOptions{
								"release-4.7": {TargetRelease: &targetRelease47},
							},
							Repos: map[string]plugins.BugzillaRepoOptions{
								"release": {
									Branches: map[string]plugins.BugzillaBranchOptions{
										"release-4.6": {TargetRelease: &targetRelease46},
									},
								},
								"release2": {
									Branches: map[string]plugins.BugzillaBranchOptions{
										"release-4.8": {TargetRelease: &targetRelease48},
									},
								},
							},
						},
					},
				},
				Cat: plugins.Cat{KeyPath: "/etc/raw"},
				Lgtm: []plugins.Lgtm{
					{Repos: []string{"openshift"}, ReviewActsAsLgtm: true},
					{Repos: []string{"openshift-priv/release"}, ReviewActsAsLgtm: true},
				},
				ExternalPlugins: map[string][]plugins.ExternalPlugin{
					"openshift": {
						{Name: "refresh", Endpoint: "http://refresh", Events: []string{"issue_comment"}},
						{Name: "cherrypick", Endpoint: "http://cherrypick", Events: []string{"issue_comment", "pull_request"}},
					},
					"openshift/release": {
						{Name: "needs-rebase", Endpoint: "http://needs-rebase", Events: []string{"issue_comment", "pull_request"}},
					},
				},
				Label: plugins.Label{
					AdditionalLabels: []string{"foo", "bar"},
					RestrictedLabels: map[string][]plugins.RestrictedLabel{
						"*":                 {{Label: "exists-everywhere", AllowedUsers: []string{"super-admin"}}},
						"openshift":         {{Label: "cherrypick-approved", AllowedTeams: []string{"patch-managers"}}},
						"openshift/release": {{Label: "manual-test-done", AllowedTeams: []string{"manual-testers"}, AllowedUsers: []string{"manual-test-bot"}}},
					},
				},
			},

			expectedConfig: &plugins.Configuration{
				Plugins: plugins.Plugins{},
				Bugzilla: plugins.Bugzilla{
					Default: map[string]plugins.BugzillaBranchOptions{
						"master": {TargetRelease: &targetRelease48},
					},
				},
				Cat: plugins.Cat{KeyPath: "/etc/raw"},
				Label: plugins.Label{
					AdditionalLabels: []string{"foo", "bar"},
					RestrictedLabels: map[string][]plugins.RestrictedLabel{"*": {{Label: "exists-everywhere", AllowedUsers: []string{"super-admin"}}}},
				},
			},
			expectedShardFiles: map[string]string{
				"openshift/_pluginconfig.yaml": strings.Join([]string{
					"approve:",
					"- commandHelpLink: \"\"",
					"  issue_required: true",
					"  repos:",
					"  - openshift",
					"bugzilla:",
					"  orgs:",
					"    openshift:",
					"      default:",
					"        release-4.6:",
					"          target_release: 4.6.0",
					"external_plugins:",
					"  openshift:",
					"  - endpoint: http://refresh",
					"    events:",
					"    - issue_comment",
					"    name: refresh",
					"  - endpoint: http://cherrypick",
					"    events:",
					"    - issue_comment",
					"    - pull_request",
					"    name: cherrypick",
					"label:",
					"  restricted_labels:",
					"    openshift:",
					"    - allowed_teams:",
					"      - patch-managers",
					"      label: cherrypick-approved",
					"lgtm:",
					"- repos:",
					"  - openshift",
					"  review_acts_as_lgtm: true",
					"plugins:",
					"  openshift:",
					"    plugins:",
					"    - foo",
					"",
				}, "\n"),
				"openshift/release/_pluginconfig.yaml": strings.Join([]string{
					"approve:",
					"- commandHelpLink: \"\"",
					"  lgtm_acts_as_approve: true",
					"  repos:",
					"  - openshift/release",
					"bugzilla:",
					"  orgs:",
					"    openshift:",
					"      repos:",
					"        release:",
					"          branches:",
					"            release-4.8:",
					"              target_release: 4.8.0",
					"external_plugins:",
					"  openshift/release:",
					"  - endpoint: http://needs-rebase",
					"    events:",
					"    - issue_comment",
					"    - pull_request",
					"    name: needs-rebase",
					"label:",
					"  restricted_labels:",
					"    openshift/release:",
					"    - allowed_teams:",
					"      - manual-testers",
					"      allowed_users:",
					"      - manual-test-bot",
					"      label: manual-test-done",
					"plugins:",
					"  openshift/release:",
					"    plugins:",
					"    - bar",
					"",
				}, "\n"),
				"openshift/release2/_pluginconfig.yaml": strings.Join([]string{
					"approve:",
					"- commandHelpLink: \"\"",
					"  lgtm_acts_as_approve: true",
					"  repos:",
					"  - openshift/release2",
					"plugins:",
					"  openshift/release2:",
					"    plugins:",
					"    - zim",
					"",
				}, "\n"),
				"openshift/release3/_pluginconfig.yaml": strings.Join([]string{
					"bugzilla:",
					"  orgs:",
					"    openshift:",
					"      repos:",
					"        release3:",
					"          branches:",
					"            release-4.7:",
					"              target_release: 4.7.0",
					"",
				}, "\n"),
				"openshift-priv/_pluginconfig.yaml": strings.Join([]string{
					"bugzilla:",
					"  orgs:",
					"    openshift-priv:",
					"      default:",
					"        release-4.7:",
					"          target_release: 4.7.0",
					"",
				}, "\n"),
				"openshift-priv/release/_pluginconfig.yaml": strings.Join([]string{
					"bugzilla:",
					"  orgs:",
					"    openshift-priv:",
					"      repos:",
					"        release:",
					"          branches:",
					"            release-4.6:",
					"              target_release: 4.6.0",
					"lgtm:",
					"- repos:",
					"  - openshift-priv/release",
					"  review_acts_as_lgtm: true",
					"",
				}, "\n"),
				"openshift-priv/release2/_pluginconfig.yaml": strings.Join([]string{
					"bugzilla:",
					"  orgs:",
					"    openshift-priv:",
					"      repos:",
					"        release2:",
					"          branches:",
					"            release-4.8:",
					"              target_release: 4.8.0",
					"",
				}, "\n"),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			serializedInitialConfig, err := yaml.Marshal(tc.in)
			if err != nil {
				t.Fatalf("failed to serialize initial config: %v", err)
			}

			afs := afero.NewMemMapFs()

			updated, err := WriteShardedPluginConfig(tc.in, afs)
			if err != nil {
				t.Fatalf("failed to shard plugin config: %v", err)
			}
			if diff := cmp.Diff(tc.expectedConfig, updated); diff != "" {
				t.Errorf("updated plugin config differs from expected: %s", diff)
			}

			shardedConfigFiles := map[string]string{}
			if err := afero.Walk(afs, "", func(path string, info fs.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return err
				}
				if filepath.Base(path) != "_pluginconfig.yaml" {
					t.Errorf("found file %s which doesn't have the expected _prowconfig.yaml name", path)
				}
				data, err := afero.ReadFile(afs, path)
				if err != nil {
					t.Errorf("failed to read file %s: %v", path, err)
				}
				shardedConfigFiles[path] = string(data)
				return nil
			}); err != nil {
				t.Errorf("walking the fs failed: %v", err)
			}

			if diff := cmp.Diff(tc.expectedShardFiles, shardedConfigFiles); diff != "" {
				t.Fatalf("actual sharded config differs from expected:\n%s", diff)
			}

			// Test that when we load the sharded config its identical to the config with which we started
			tempDir := t.TempDir()

			// We need to write and load the initial config to put it through defaulting
			if err := os.WriteFile(filepath.Join(tempDir, "_original_config.yaml"), serializedInitialConfig, 0644); err != nil {
				t.Fatalf("failed to write out serialized initial config: %v", err)
			}
			// Defaulting is unexported and only happens inside plugins.ConfigAgent.Load()
			initialConfigAgent := plugins.ConfigAgent{}
			if err := initialConfigAgent.Start(filepath.Join(tempDir, "_original_config.yaml"), nil, "", false, false); err != nil {
				t.Fatalf("failed to start old plugin config agent: %v", err)
			}

			serializedNewConfig, err := yaml.Marshal(updated)
			if err != nil {
				t.Fatalf("failed to marshal the new config: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tempDir, "_plugins.yaml"), serializedNewConfig, 0644); err != nil {
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
			opts := pluginsflagutil.PluginOptions{}
			opts.AddFlags(fs)
			if err := fs.Parse([]string{
				"--plugin-config=" + filepath.Join(tempDir, "_plugins.yaml"),
				"--supplemental-plugin-config-dir=" + tempDir,
			}); err != nil {
				t.Fatalf("failed to parse flags")
			}

			pluginAgent, err := opts.PluginAgent()
			if err != nil {
				t.Fatalf("failed to construct plugin agent: %v", err)
			}
			if diff := cmp.Diff(initialConfigAgent.Config(), pluginAgent.Config(), cmp.Exporter(func(_ reflect.Type) bool { return true })); diff != "" {
				t.Errorf("initial config differs from what we got when sharding: %s", diff)
			}
		})
	}
}
