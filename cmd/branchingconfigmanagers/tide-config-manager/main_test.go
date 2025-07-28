package main

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/shardprowconfig"
	"github.com/openshift/ci-tools/pkg/config"
)

func prepareProwConfig(repos, labels, branches []string) string {
	return prepareProwConfigWithExcludedBranches(repos, labels, branches, []string{})
}

func prepareProwConfigWithExcludedBranches(repos, labels, branches, excludedBranches []string) string {
	config := &shardprowconfig.ProwConfigWithPointers{Tide: &shardprowconfig.TideConfig{Queries: prowconfig.TideQueries{
		{
			Repos:            repos,
			Labels:           labels,
			IncludedBranches: branches,
			ExcludedBranches: excludedBranches,
		},
	}}}
	bytes, err := yaml.Marshal(config)
	if err != nil {
		panic("Error marshaling prow config for comparison")
	}
	return string(bytes)
}

func TestReconcile(t *testing.T) {
	path := "openshift/dummy/_prowconfig.yaml"
	repos := []string{"openshift/dummy"}

	ocpCurrentVersion := "4.9"

	type args struct {
		event  string
		config *prowconfig.ProwConfig
	}
	tests := []struct {
		name               string
		args               args
		wantErr            bool
		expectedShardFiles map[string]string
	}{
		{
			name: "acknowledge-critical-fixes-only added during special event: ackCriticalFixes",
			args: args{
				event: ackCriticalFixes,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{"lgtm", "approved"},
						IncludedBranches: []string{"main", "master"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{ackCriticalFixes, "approved", "lgtm"}, []string{"main", "master"})},
		},
		{
			name: "acknowledge-critical-fixes-only removed during special event: revertCriticalFixes",
			args: args{
				event: revertCriticalFixes,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{"lgtm", "approved", ackCriticalFixes},
						IncludedBranches: []string{"main", "master"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{"approved", "lgtm"}, []string{"main", "master"})},
		},
		{
			name: "staff-eng-approved replaced by backport-risk-assesed during branching",
			args: args{
				event: branching,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed}, []string{"openshift-4.9", "release-4.9"})},
		},
		{
			name: "staff-eng-approved replaced by backport-risk-assesed during branching - for release",
			args: args{
				event: branching,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed}, []string{"release-4.9"})},
		},
		{
			name: "staff-eng-approved added with backport-risk-assesed prserved pre GA",
			args: args{
				event: preGeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{backportRiskAssessed},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed, staffEngApproved}, []string{"openshift-4.9", "release-4.9"})},
		},
		{
			name: "staff-eng-approved added with backport-risk-assesed prserved pre GA - only openshift",
			args: args{
				event: preGeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{backportRiskAssessed},
						IncludedBranches: []string{"openshift-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed, staffEngApproved}, []string{"openshift-4.9"})},
		},
		{
			name: "staff-eng-approved changes current to future during GA",
			args: args{
				event: GeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved, backportRiskAssessed},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{staffEngApproved}, []string{"openshift-4.10", "release-4.10"})},
		},
		{
			name: "staff-eng-approved changes current to future during GA",
			args: args{
				event: GeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{staffEngApproved}, []string{"openshift-4.10", "release-4.10"})},
		},
		{
			name: "past release excluded branches complemented with current and future during GA",
			args: args{
				event: GeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						ExcludedBranches: []string{"openshift-4.8", "release-4.8"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfigWithExcludedBranches(
					repos,
					[]string{},
					[]string{},
					[]string{"openshift-4.10", "openshift-4.8", "openshift-4.9", "release-4.10", "release-4.8", "release-4.9"})},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := afero.NewMemMapFs()
			if err := reconcile(ocpCurrentVersion, tt.args.event, tt.args.config, target, excludedRepos{}, repos, "", "", ""); (err != nil) != tt.wantErr {
				t.Errorf("reconcile() error = %v, wantErr %v", err, tt.wantErr)
			}
			shardedConfigFiles := map[string]string{}
			if err := afero.Walk(target, "", func(path string, info fs.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return err
				}
				data, err := afero.ReadFile(target, path)
				if err != nil {
					t.Errorf("failed to read file %s: %v", path, err)
				}
				shardedConfigFiles[path] = string(data)
				return nil
			}); err != nil {
				t.Errorf("waking the fs failed: %v", err)
			}
			if diff := cmp.Diff(tt.expectedShardFiles, shardedConfigFiles); diff != "" {
				t.Fatalf("actual sharded config differs from expected:\n%s", diff)
			}

		})
	}
}

func TestNewVerifiedEvent(t *testing.T) {
	delegate := newSharedDataDelegate()

	tests := []struct {
		name              string
		optInRepos        []string
		optOutRepos       []string
		ciOperatorConfigs map[string]string
		expectedOptIn     []string
		wantErr           bool
	}{
		{
			name:              "opt-in repos only",
			optInRepos:        []string{"openshift/dummy", "openshift/test"},
			optOutRepos:       []string{},
			ciOperatorConfigs: map[string]string{},
			expectedOptIn:     []string{"openshift/dummy", "openshift/test"},
			wantErr:           false,
		},
		{
			name:        "ci-operator configs with ocp promotion",
			optInRepos:  []string{},
			optOutRepos: []string{},
			ciOperatorConfigs: map[string]string{
				"openshift/dummy": "ocp",
				"openshift/test":  "other-namespace",
			},
			expectedOptIn: []string{"openshift/dummy"},
			wantErr:       false,
		},
		{
			name:        "opt-out excludes repos",
			optInRepos:  []string{"openshift/dummy", "openshift/test"},
			optOutRepos: []string{"openshift/dummy"},
			ciOperatorConfigs: map[string]string{
				"openshift/excluded": "ocp",
			},
			expectedOptIn: []string{"openshift/test", "openshift/excluded"},
			wantErr:       false,
		},
		{
			name:        "opt-out excludes ci-operator detected repos",
			optInRepos:  []string{},
			optOutRepos: []string{"openshift/dummy"},
			ciOperatorConfigs: map[string]string{
				"openshift/dummy": "ocp",
				"openshift/test":  "ocp",
			},
			expectedOptIn: []string{"openshift/test"},
			wantErr:       false,
		},
		{
			name:              "empty files",
			optInRepos:        []string{},
			optOutRepos:       []string{},
			ciOperatorConfigs: map[string]string{},
			expectedOptIn:     []string{},
			wantErr:           false,
		},
		{
			name:              "file reader error",
			optInRepos:        nil,
			optOutRepos:       []string{},
			ciOperatorConfigs: map[string]string{},
			expectedOptIn:     []string{},
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fileReader := func(filePath string) ([]string, error) {
				if tt.optInRepos == nil {
					return nil, fmt.Errorf("mock file read error")
				}
				if strings.Contains(filePath, "opt-in") {
					return tt.optInRepos, nil
				}
				if strings.Contains(filePath, "opt-out") {
					return tt.optOutRepos, nil
				}
				return []string{}, nil
			}

			configDirOperator := func(dir string, callback config.ConfigIterFunc) error {
				for orgRepo, namespace := range tt.ciOperatorConfigs {
					parts := strings.Split(orgRepo, "/")
					if len(parts) != 2 {
						continue
					}

					cfg := &api.ReleaseBuildConfiguration{}
					if namespace != "" {
						cfg.PromotionConfiguration = &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{
								{Namespace: namespace},
							},
						}
					}

					info := &config.Info{
						Metadata: api.Metadata{
							Org:  parts[0],
							Repo: parts[1],
						},
					}

					if err := callback(cfg, info); err != nil {
						return err
					}
				}
				return nil
			}

			result, err := newVerifiedEventWithDeps(
				"opt-in-file",
				"opt-out-file",
				"ci-config-dir",
				delegate,
				fileReader,
				configDirOperator,
			)

			if (err != nil) != tt.wantErr {
				t.Errorf("newVerifiedEventWithDeps() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			actualOptIn := result.optInRepos.UnsortedList()
			expectedOptIn := tt.expectedOptIn

			if len(actualOptIn) != len(expectedOptIn) {
				t.Errorf("Expected %d opt-in repos, got %d", len(expectedOptIn), len(actualOptIn))
				t.Errorf("Expected: %v", expectedOptIn)
				t.Errorf("Actual: %v", actualOptIn)
				return
			}

			actualSet := sets.New[string](actualOptIn...)
			expectedSet := sets.New[string](expectedOptIn...)

			if !actualSet.Equal(expectedSet) {
				t.Errorf("Expected opt-in repos: %v, got: %v", expectedOptIn, actualOptIn)
			}
		})
	}
}

func TestVerifiedEventModifyQuery(t *testing.T) {
	delegate := newSharedDataDelegate()

	tests := []struct {
		name           string
		optInRepos     []string
		optOutRepos    []string
		repo           string
		query          *prowconfig.TideQuery
		expectedLabels []string
	}{
		{
			name:        "adds verified label for opt-in repo on main branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"main"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "adds verified label for opt-in repo on master branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"master"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "does not add verified label for non-main/master branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"feature-branch"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
		{
			name:        "does not add verified label for repo not in opt-in",
			optInRepos:  []string{"openshift/other"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"main"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
		{
			name:        "does not add verified label for opt-out repo",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{"openshift/dummy"},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"main"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
		{
			name:        "adds verified label for release-4.x branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"release-4.9"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "adds verified label for openshift-4.x branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"openshift-4.10"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "adds verified label for multiple versioned branches",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"release-4.9", "openshift-4.10"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "does not add verified label for non-versioned release branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"release-v4.9"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
		{
			name:        "does not add verified label for invalid version branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"release-4.abc"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ve := &verifiedEvent{
				optInRepos:         sets.New[string](tt.optInRepos...),
				optOutRepos:        sets.New[string](tt.optOutRepos...),
				sharedDataDelegate: delegate,
			}

			ve.ModifyQuery(tt.query, tt.repo)

			actualLabels := tt.query.Labels
			expectedLabels := tt.expectedLabels

			if len(actualLabels) != len(expectedLabels) {
				t.Errorf("Expected %d labels, got %d", len(expectedLabels), len(actualLabels))
				t.Errorf("Expected: %v", expectedLabels)
				t.Errorf("Actual: %v", actualLabels)
				return
			}

			actualSet := sets.New[string](actualLabels...)
			expectedSet := sets.New[string](expectedLabels...)

			if !actualSet.Equal(expectedSet) {
				t.Errorf("Expected labels: %v, got: %v", expectedLabels, actualLabels)
			}
		})
	}
}

func TestIsVersionedBranch(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		expected bool
	}{
		{
			name:     "valid release-4.x branch",
			branch:   "release-4.9",
			expected: true,
		},
		{
			name:     "valid openshift-4.x branch",
			branch:   "openshift-4.10",
			expected: true,
		},
		{
			name:     "valid release branch with double digit minor",
			branch:   "release-4.15",
			expected: true,
		},
		{
			name:     "valid openshift branch with double digit minor",
			branch:   "openshift-4.15",
			expected: true,
		},
		{
			name:     "invalid release branch with version 0",
			branch:   "release-4.0",
			expected: false,
		},
		{
			name:     "invalid openshift branch with version 0",
			branch:   "openshift-4.0",
			expected: false,
		},
		{
			name:     "invalid release branch with non-numeric version",
			branch:   "release-4.abc",
			expected: false,
		},
		{
			name:     "invalid openshift branch with non-numeric version",
			branch:   "openshift-4.x",
			expected: false,
		},
		{
			name:     "invalid release branch with v prefix",
			branch:   "release-v4.9",
			expected: false,
		},
		{
			name:     "invalid openshift branch with v prefix",
			branch:   "openshift-v4.9",
			expected: false,
		},
		{
			name:     "invalid branch with release prefix but wrong format",
			branch:   "release-3.9",
			expected: false,
		},
		{
			name:     "invalid branch with openshift prefix but wrong format",
			branch:   "openshift-3.9",
			expected: false,
		},
		{
			name:     "main branch",
			branch:   "main",
			expected: false,
		},
		{
			name:     "master branch",
			branch:   "master",
			expected: false,
		},
		{
			name:     "empty branch",
			branch:   "",
			expected: false,
		},
		{
			name:     "release branch without version",
			branch:   "release-4.",
			expected: false,
		},
		{
			name:     "openshift branch without version",
			branch:   "openshift-4.",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isVersionedBranch(tt.branch)
			if result != tt.expected {
				t.Errorf("isVersionedBranch(%q) = %v, expected %v", tt.branch, result, tt.expected)
			}
		})
	}
}
