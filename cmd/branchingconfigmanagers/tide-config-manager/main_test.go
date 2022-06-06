package main

import (
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"

	"k8s.io/test-infra/prow/config"
	prowconfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/api/shardprowconfig"
)

func prepareProwConfig(repos, labels, branches []string) string {
	return prepareProwConfigWithExcludedBranches(repos, labels, branches, []string{})
}

func prepareProwConfigWithExcludedBranches(repos, labels, branches, excludedBranches []string) string {
	config := &shardprowconfig.ProwConfigWithPointers{Tide: &shardprowconfig.TideConfig{Queries: config.TideQueries{
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
	privPath := "openshift-priv/dummy/_prowconfig.yaml"

	repos := []string{"openshift/dummy"}
	privRepos := []string{"openshift-priv/dummy"}

	featureFreezeEvent := &ocplifecycle.Event{
		LifecyclePhase: ocplifecycle.LifecyclePhase{
			Event: ocplifecycle.LifecycleEventFeatureFreeze},
		ProductVersion: "4.9"}
	codeFreezeEvent := &ocplifecycle.Event{
		LifecyclePhase: ocplifecycle.LifecyclePhase{
			Event: ocplifecycle.LifecycleEventCodeFreeze},
		ProductVersion: "4.9"}
	generalAvailabilityEvent := &ocplifecycle.Event{
		LifecyclePhase: ocplifecycle.LifecyclePhase{
			Event: ocplifecycle.LifecycleEventGenerallyAvailable},
		ProductVersion: "4.9"}

	type args struct {
		event  *ocplifecycle.Event
		config *prowconfig.ProwConfig
		target afero.Fs
	}
	tests := []struct {
		name               string
		args               args
		wantErr            bool
		expectedShardFiles map[string]string
	}{
		{
			name: "qe-approved will trigger addition px-approved and docs-approved",
			args: args{
				target: afero.NewMemMapFs(),
				event:  featureFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{qeApproved},
						IncludedBranches: []string{masterBranch},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{docsApproved, pxApproved, qeApproved}, []string{masterBranch})},
		},
		{
			name: "label cherry-pick-approved together with branches main and release will trigger addition of bugzilla/valid-bug label",
			args: args{
				target: afero.NewMemMapFs(),
				event:  featureFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{cherryPickApproved},
						IncludedBranches: []string{mainBranch, "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{validBug, cherryPickApproved}, []string{mainBranch, "release-4.9"})},
		},
		{
			name: "openshift-priv repo will not be modified",
			args: args{
				target: afero.NewMemMapFs(),
				event:  featureFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            privRepos,
						Labels:           []string{cherryPickApproved},
						IncludedBranches: []string{mainBranch, "openshift-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				privPath: prepareProwConfig(privRepos, []string{cherryPickApproved}, []string{mainBranch, "openshift-4.9"})},
		},
		{
			name: "lack of main/master won't trigger modification",
			args: args{
				target: afero.NewMemMapFs(),
				event:  featureFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{cherryPickApproved},
						IncludedBranches: []string{"openshift-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{cherryPickApproved}, []string{"openshift-4.9"})},
		},
		{
			name: "lack of cherry-pick-approved label won't trigger modification",
			args: args{
				target: afero.NewMemMapFs(),
				event:  featureFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						IncludedBranches: []string{masterBranch, "openshift-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{}, []string{masterBranch, "openshift-4.9"})},
		},
		{
			name: "bugzilla/valid-bug will be removed during code freeze from repo with main",
			args: args{
				target: afero.NewMemMapFs(),
				event:  codeFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{validBug},
						IncludedBranches: []string{mainBranch, "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{}, []string{mainBranch, "release-4.9"})},
		},
		{
			name: "bugzilla/valid-bug will not be removed as repo is no feature freeze due to a qe-approved label",
			args: args{
				target: afero.NewMemMapFs(),
				event:  codeFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{validBug, qeApproved},
						IncludedBranches: []string{mainBranch, "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{validBug, qeApproved}, []string{mainBranch, "release-4.9"})},
		},
		{
			name: "no master or main will lead to ignoring the config during code freeze",
			args: args{
				target: afero.NewMemMapFs(),
				event:  codeFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{validBug},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{validBug}, []string{"openshift-4.9", "release-4.9"})},
		},
		{
			name: "staff-eng-approved changes current to future during GA",
			args: args{
				target: afero.NewMemMapFs(),
				event:  generalAvailabilityEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{staffEngApproved}, []string{"openshift-4.10", "release-4.10"})},
		},
		{
			name: "staff-eng-approved changes current to future during GA",
			args: args{
				target: afero.NewMemMapFs(),
				event:  generalAvailabilityEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{staffEngApproved}, []string{"openshift-4.10", "release-4.10"})},
		},
		{
			name: "cherry-pick-approved changes past release to current during GA",
			args: args{
				target: afero.NewMemMapFs(),
				event:  generalAvailabilityEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{cherryPickApproved},
						IncludedBranches: []string{"openshift-4.8", "release-4.8"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{cherryPickApproved}, []string{"openshift-4.8", "openshift-4.9", "release-4.8", "release-4.9"})},
		},
		{
			name: "past release excluded branches complemented with current and future during GA",
			args: args{
				target: afero.NewMemMapFs(),
				event:  generalAvailabilityEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            repos,
						ExcludedBranches: []string{"openshift-4.8", "release-4.8"},
					},
				}}},
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
			if err := reconcile(tt.args.event, tt.args.config, tt.args.target, excludedRepos{}); (err != nil) != tt.wantErr {
				t.Errorf("reconcile() error = %v, wantErr %v", err, tt.wantErr)
			}
			shardedConfigFiles := map[string]string{}
			if err := afero.Walk(tt.args.target, "", func(path string, info fs.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return err
				}
				data, err := afero.ReadFile(tt.args.target, path)
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
