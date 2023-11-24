package main

import (
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"

	prowconfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/shardprowconfig"
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
			name: "staff-eng-approved replaced by backport-risk-assesed and cherry-pick-approved during branching",
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
				path: prepareProwConfig(repos, []string{backportRiskAssessed, cherryPickApproved}, []string{"openshift-4.9", "release-4.9"})},
		},
		{
			name: "staff-eng-approved replaced by backport-risk-assesed and cherry-pick-approved during branching - for release",
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
				path: prepareProwConfig(repos, []string{backportRiskAssessed, cherryPickApproved}, []string{"release-4.9"})},
		},
		{
			name: "staff-eng-approved added with backport-risk-assesed and cherry-pick-approved prserved pre GA",
			args: args{
				event: preGeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{backportRiskAssessed, cherryPickApproved},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed, cherryPickApproved, staffEngApproved}, []string{"openshift-4.9", "release-4.9"})},
		},
		{
			name: "staff-eng-approved added with backport-risk-assesed and cherry-pick-approved prserved pre GA - only openshift",
			args: args{
				event: preGeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{backportRiskAssessed, cherryPickApproved},
						IncludedBranches: []string{"openshift-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed, cherryPickApproved, staffEngApproved}, []string{"openshift-4.9"})},
		},
		{
			name: "staff-eng-approved changes current to future during GA",
			args: args{
				event: GeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved, backportRiskAssessed, cherryPickApproved},
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
			name: "cherry-pick-approved changes past release to current during GA",
			args: args{
				event: GeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{cherryPickApproved},
						IncludedBranches: []string{"openshift-4.8", "release-4.8"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{cherryPickApproved}, []string{"openshift-4.8", "openshift-4.9", "release-4.8", "release-4.9"})},
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
			if err := reconcile(ocpCurrentVersion, tt.args.event, tt.args.config, target, excludedRepos{}, repos); (err != nil) != tt.wantErr {
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
