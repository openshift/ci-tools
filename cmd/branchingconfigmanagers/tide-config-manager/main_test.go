package main

import (
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"

	"k8s.io/test-infra/prow/config"
	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

func TestReconcile(t *testing.T) {
	featureFreezeEvent := &ocplifecycle.Event{
		LifecyclePhase: ocplifecycle.LifecyclePhase{
			Event: ocplifecycle.LifecycleEventFeatureFreeze},
		ProductVersion: "4.9"}
	codeFreezeEvent := &ocplifecycle.Event{
		LifecyclePhase: ocplifecycle.LifecyclePhase{
			Event: ocplifecycle.LifecycleEventCodeFreeze},
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
						Repos:            []string{"openshift/dummy"},
						Labels:           []string{"qe-approved"},
						IncludedBranches: []string{"master"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				"openshift/dummy/_prowconfig.yaml": `tide:
  queries:
  - includedBranches:
    - master
    labels:
    - docs-approved
    - px-approved
    - qe-approved
    repos:
    - openshift/dummy
`},
		},
		{
			name: "label cherry-pick-approved together with branches main and release will trigger addition of bugzilla/valid-bug label",
			args: args{
				target: afero.NewMemMapFs(),
				event:  featureFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            []string{"openshift/dummy"},
						Labels:           []string{"cherry-pick-approved"},
						IncludedBranches: []string{"main", "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				"openshift/dummy/_prowconfig.yaml": `tide:
  queries:
  - includedBranches:
    - main
    - release-4.9
    labels:
    - bugzilla/valid-bug
    - cherry-pick-approved
    repos:
    - openshift/dummy
`},
		},
		{
			name: "openshift-priv repo will not be modified",
			args: args{
				target: afero.NewMemMapFs(),
				event:  featureFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            []string{"openshift-priv/dummy"},
						Labels:           []string{"cherry-pick-approved"},
						IncludedBranches: []string{"main", "openshift-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				"openshift-priv/dummy/_prowconfig.yaml": `tide:
  queries:
  - includedBranches:
    - main
    - openshift-4.9
    labels:
    - cherry-pick-approved
    repos:
    - openshift-priv/dummy
`},
		},
		{
			name: "lack of main/master won't trigger modification",
			args: args{
				target: afero.NewMemMapFs(),
				event:  featureFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            []string{"openshift/dummy"},
						Labels:           []string{"cherry-pick-approved"},
						IncludedBranches: []string{"openshift-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				"openshift/dummy/_prowconfig.yaml": `tide:
  queries:
  - includedBranches:
    - openshift-4.9
    labels:
    - cherry-pick-approved
    repos:
    - openshift/dummy
`},
		},
		{
			name: "lack of cherry-pick-approved label won't trigger modification",
			args: args{
				target: afero.NewMemMapFs(),
				event:  featureFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            []string{"openshift/dummy"},
						IncludedBranches: []string{"master", "openshift-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				"openshift/dummy/_prowconfig.yaml": `tide:
  queries:
  - includedBranches:
    - master
    - openshift-4.9
    repos:
    - openshift/dummy
`},
		},
		{
			name: "bugzilla/valid-bug will be removed during code freeze from repo with main",
			args: args{
				target: afero.NewMemMapFs(),
				event:  codeFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            []string{"openshift/dummy"},
						Labels:           []string{"bugzilla/valid-bug"},
						IncludedBranches: []string{"main", "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				"openshift/dummy/_prowconfig.yaml": `tide:
  queries:
  - includedBranches:
    - main
    - release-4.9
    repos:
    - openshift/dummy
`},
		},
		{
			name: "bugzilla/valid-bug will not be removed as repo is no feature freeze due to a qe-approved label",
			args: args{
				target: afero.NewMemMapFs(),
				event:  codeFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            []string{"openshift/dummy"},
						Labels:           []string{"bugzilla/valid-bug", "qe-approved"},
						IncludedBranches: []string{"main", "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				"openshift/dummy/_prowconfig.yaml": `tide:
  queries:
  - includedBranches:
    - main
    - release-4.9
    labels:
    - bugzilla/valid-bug
    - qe-approved
    repos:
    - openshift/dummy
`},
		},
		{
			name: "no master or main will lead to ignoring the config during code freeze",
			args: args{
				target: afero.NewMemMapFs(),
				event:  codeFreezeEvent,
				config: &prowconfig.ProwConfig{Tide: config.Tide{Queries: config.TideQueries{
					{
						Repos:            []string{"openshift/dummy"},
						Labels:           []string{"bugzilla/valid-bug"},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				"openshift/dummy/_prowconfig.yaml": `tide:
  queries:
  - includedBranches:
    - openshift-4.9
    - release-4.9
    labels:
    - bugzilla/valid-bug
    repos:
    - openshift/dummy
`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := reconcile(tt.args.event, tt.args.config, tt.args.target); (err != nil) != tt.wantErr {
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
