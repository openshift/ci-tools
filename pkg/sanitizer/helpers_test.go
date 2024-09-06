package sanitizer

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
)

const build01 = "build01"
const build02 = "build02"

func TestFindMostUsedCluster(t *testing.T) {

	tests := []struct {
		name      string
		jobConfig prowconfig.JobConfig
		expected  string
	}{
		{
			name: "no jobs",
			jobConfig: prowconfig.JobConfig{
				PresubmitsStatic:  map[string][]prowconfig.Presubmit{},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
				Periodics:         []prowconfig.Periodic{},
			},
			expected: "",
		},
		{
			name: "single presubmit job",
			jobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo1": {
						{JobBase: prowconfig.JobBase{Cluster: build01}},
					},
				},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
				Periodics:         []prowconfig.Periodic{},
			},
			expected: build01,
		},
		{
			name: "multiple jobs same cluster",
			jobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo1": {
						{JobBase: prowconfig.JobBase{Cluster: build01}},
					},
				},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo1": {
						{JobBase: prowconfig.JobBase{Cluster: build01}},
					},
				},
				Periodics: []prowconfig.Periodic{
					{JobBase: prowconfig.JobBase{Cluster: build01}},
				},
			},
			expected: build01,
		},
		{
			name: "multiple jobs different clusters",
			jobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo1": {
						{JobBase: prowconfig.JobBase{Cluster: build01}},
					},
				},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo2": {
						{JobBase: prowconfig.JobBase{Cluster: build02}},
					},
				},
				Periodics: []prowconfig.Periodic{
					{JobBase: prowconfig.JobBase{Cluster: build01}},
				},
			},
			expected: build01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindMostUsedCluster(&tt.jobConfig)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestDetermineTargetCluster(t *testing.T) {
	type fields struct {
		blocked sets.Set[string]
	}
	type args struct {
		cluster           string
		determinedCluster string
		defaultCluster    string
		canBeRelocated    bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   string
	}{
		{
			name: "relocate to cluster for a test group",
			fields: fields{
				blocked: sets.New[string](),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    true,
			},
			want: "build01",
		},
		{
			name: "can't relocate to cluster for a test group",
			fields: fields{
				blocked: sets.New[string](),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    false,
			},
			want: "build02",
		},
		{
			name: "both clusters are blocked, relocate to default",
			fields: fields{
				blocked: sets.New[string]("build01", "build02"),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    false,
			},
			want: "build03",
		},
		{
			name: "determined is blocked, relocate to a group cluster despite canBeRelocated=false",
			fields: fields{
				blocked: sets.New[string]("build02"),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    false,
			},
			want: "build01",
		},
		{
			name: "group cluster is blocked, use determined cluster",
			fields: fields{
				blocked: sets.New[string]("build01"),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    false,
			},
			want: "build02",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetermineTargetCluster(tt.args.cluster, tt.args.determinedCluster, tt.args.defaultCluster, tt.args.canBeRelocated, tt.fields.blocked); got != tt.want {
				t.Errorf("clusterVolume.determineCluster() = %v, want %v", got, tt.want)
			}
		})
	}
}
