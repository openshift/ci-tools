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

func TestLoadClusterConfigFromBytes(t *testing.T) {
	tests := []struct {
		name            string
		yamlData        string
		expectedCluster ClusterMap
		expectedBlocked sets.Set[string]
	}{
		{
			name: "Valid config with AWS and GCP",
			yamlData: `
aws:
  - name: build01
    capacity: 80
    capabilities:
      - aarch64
      - amd64
      - vpn
  - name: build03
  - name: build09
    blocked: true
  - name: build99
    capacity: -99 #will be blocked as well
gcp:
  - name: build02
    capacity: 60
    capabilities:
      - vpn
`,
			expectedCluster: ClusterMap{
				"build01": {
					Provider:     "aws",
					Capacity:     80,
					Capabilities: []string{"aarch64", "amd64", "vpn"},
				},
				"build03": {
					Provider:     "aws",
					Capacity:     100,
					Capabilities: nil,
				},
				"build02": {
					Provider:     "gcp",
					Capacity:     60,
					Capabilities: []string{"vpn"},
				},
			},
			expectedBlocked: sets.New[string]("build09", "build99"),
		},
		{
			name: "Config with missing capacities and capabilities",
			yamlData: `
aws:
  - name: build01
    capacity: 101 #capacity to 100
gcp:
  - name: build02
    capabilities:
      - vpn
  - name: build03
    blocked: true
`,
			expectedCluster: ClusterMap{
				"build01": {
					Provider:     "aws",
					Capacity:     100,
					Capabilities: nil,
				},
				"build02": {
					Provider:     "gcp",
					Capacity:     100,
					Capabilities: []string{"vpn"},
				},
			},
			expectedBlocked: sets.New[string]("build03"),
		},
		{
			name: "Empty config",
			yamlData: `
aws: []
gcp: []
`,
			expectedCluster: ClusterMap{},
			expectedBlocked: sets.New[string](),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(tt.yamlData)

			clusterMap, blockedClusters, err := loadClusterConfigFromBytes(data)
			if err != nil {
				t.Fatalf("Failed to load cluster config: %v", err)
			}

			for clusterName, expectedInfo := range tt.expectedCluster {
				if info, exists := clusterMap[clusterName]; !exists {
					t.Errorf("Expected cluster %s to be in clusterMap", clusterName)
				} else {
					if info.Provider != expectedInfo.Provider {
						t.Errorf("Expected provider for %s: %s, got: %s", clusterName, expectedInfo.Provider, info.Provider)
					}
					if info.Capacity != expectedInfo.Capacity {
						t.Errorf("Expected capacity for %s: %d, got: %d", clusterName, expectedInfo.Capacity, info.Capacity)
					}
					if len(info.Capabilities) != len(expectedInfo.Capabilities) {
						t.Errorf("Expected capabilities length for %s: %d, got: %d", clusterName, len(expectedInfo.Capabilities), len(info.Capabilities))
					}
					for i, capability := range info.Capabilities {
						if capability != expectedInfo.Capabilities[i] {
							t.Errorf("Expected capability %d for %s: %s, got: %s", i, clusterName, expectedInfo.Capabilities[i], capability)
						}
					}
				}
			}
			if !blockedClusters.Equal(tt.expectedBlocked) {
				t.Errorf("Expected blocked clusters: %v, got: %v", tt.expectedBlocked.UnsortedList(), blockedClusters.UnsortedList())
			}
		})
	}
}
