package dispatcher

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/testhelper"
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
			actual := FindMostUsedCluster(&tt.jobConfig)
			if diff := cmp.Diff(tt.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tt.name, diff)
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
			actual := DetermineTargetCluster(tt.args.cluster, tt.args.determinedCluster, tt.args.defaultCluster, tt.args.canBeRelocated, tt.fields.blocked)
			if diff := cmp.Diff(tt.want, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tt.name, diff)
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
		expectedError   error
	}{
		{
			name: "Valid config with AWS and GCP",
			yamlData: `
aws:
  - name: build01
    capacity: 80
    ipCapacity: 59
    capabilities:
      - aarch64
      - amd64
      - intranet
  - name: build03
    ipCapacity: 59
  - name: build09
    blocked: true
  - name: build99
    capacity: -99 #will be blocked as well
gcp:
  - name: build02
    capacity: 60
    ipCapacity: 100
    capabilities:
      - intranet
`,
			expectedCluster: ClusterMap{
				"build01": {
					Provider:     "aws",
					Capacity:     80,
					IPCapacity:   59,
					Capabilities: []string{"aarch64", "amd64", "intranet"},
				},
				"build03": {
					Provider:     "aws",
					Capacity:     100,
					IPCapacity:   59,
					Capabilities: nil,
				},
				"build02": {
					Provider:     "gcp",
					Capacity:     60,
					IPCapacity:   100,
					Capabilities: []string{"intranet"},
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
      - intranet
  - name: build03
    blocked: true
`,
			expectedCluster: ClusterMap{
				"build01": {
					Provider:     "aws",
					Capacity:     100,
					IPCapacity:   0,
					Capabilities: nil,
				},
				"build02": {
					Provider:     "gcp",
					Capacity:     100,
					IPCapacity:   0,
					Capabilities: []string{"intranet"},
				},
			},
			expectedBlocked: sets.New[string]("build03"),
		},
		{
			name: "omitted ipCapacity is zero and does not fail when no cluster sets it",
			yamlData: `
aws:
  - name: build01
    capacity: 50
gcp:
  - name: build02
    capacity: 100
`,
			expectedCluster: ClusterMap{
				"build01": {
					Provider: "aws",
					Capacity: 50,
				},
				"build02": {
					Provider: "gcp",
					Capacity: 100,
				},
			},
			expectedBlocked: sets.New[string](),
		},
		{
			name: "mixed ipCapacity loads when only some clusters set it",
			yamlData: `
aws:
  - name: build01
    capacity: 50
    ipCapacity: 100
gcp:
  - name: build02
    capacity: 100
`,
			expectedCluster: ClusterMap{
				"build01": {
					Provider:   "aws",
					Capacity:   50,
					IPCapacity: 100,
				},
				"build02": {
					Provider: "gcp",
					Capacity: 100,
				},
			},
			expectedBlocked: sets.New[string](),
		},
		{
			name: "negative ipCapacity fails",
			yamlData: `
aws:
  - name: build01
    capacity: 50
    ipCapacity: -1
`,
			expectedError: fmt.Errorf(`cluster "build01" has negative ipCapacity: -1`),
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
			clusterMap, blockedClusters, err := loadClusterConfigFromBytes([]byte(tt.yamlData))
			if diff := cmp.Diff(tt.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tt.name, diff)
			}
			if err != nil {
				return
			}
			if diff := cmp.Diff(tt.expectedCluster, clusterMap); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tt.name, diff)
			}
			if diff := cmp.Diff(sets.List(tt.expectedBlocked), sets.List(blockedClusters)); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tt.name, diff)
			}
		})
	}
}

func TestHasCapacityOrCapabilitiesChanged(t *testing.T) {
	tests := []struct {
		name     string
		prev     ClusterMap
		next     ClusterMap
		expected bool
	}{
		{
			name: "No change in capacity or capabilities",
			prev: ClusterMap{
				"build01": {Provider: "AWS", Capacity: 10, Capabilities: []string{"aarch64", "intranet", "rce", "sshd-bastion"}}, // rce - release-controller-eligible, sshd-bastion - for multiarch P/Z libvirt jobs
				"build02": {Provider: "GCP", Capacity: 20, Capabilities: []string{"amd64", "intranet", "rce", "sshd-bastion"}},
			},
			next: ClusterMap{
				"build01": {Provider: "AWS", Capacity: 10, Capabilities: []string{"aarch64", "intranet", "rce", "sshd-bastion"}},
				"build02": {Provider: "GCP", Capacity: 20, Capabilities: []string{"amd64", "intranet", "rce", "sshd-bastion"}},
			},
			expected: false,
		},
		{
			name: "Change in capacity for build01",
			prev: ClusterMap{
				"build01": {Provider: "AWS", Capacity: 10, Capabilities: []string{"aarch64", "intranet", "rce", "sshd-bastion"}},
				"build02": {Provider: "GCP", Capacity: 20, Capabilities: []string{"amd64", "intranet", "rce", "sshd-bastion"}},
			},
			next: ClusterMap{
				"build01": {Provider: "AWS", Capacity: 15, Capabilities: []string{"aarch64", "intranet", "rce", "sshd-bastion"}},
				"build02": {Provider: "GCP", Capacity: 20, Capabilities: []string{"amd64", "intranet", "rce", "sshd-bastion"}},
			},
			expected: true,
		},
		{
			name: "Change in capabilities for build02",
			prev: ClusterMap{
				"build01": {Provider: "AWS", Capacity: 10, Capabilities: []string{"aarch64", "intranet", "rce", "sshd-bastion"}},
				"build02": {Provider: "GCP", Capacity: 20, Capabilities: []string{"amd64", "intranet", "rce", "sshd-bastion"}},
			},
			next: ClusterMap{
				"build01": {Provider: "AWS", Capacity: 10, Capabilities: []string{"aarch64", "intranet", "rce", "sshd-bastion"}},
				"build02": {Provider: "GCP", Capacity: 20, Capabilities: []string{"aarch64", "intranet", "rce", "sshd-bastion"}},
			},
			expected: true,
		},
		{
			name: "Change in ipCapacity for build01",
			prev: ClusterMap{
				"build01": {Provider: "AWS", Capacity: 10, IPCapacity: 40, Capabilities: []string{"aarch64"}},
				"build02": {Provider: "GCP", Capacity: 20, IPCapacity: 100, Capabilities: []string{"amd64"}},
			},
			next: ClusterMap{
				"build01": {Provider: "AWS", Capacity: 10, IPCapacity: 80, Capabilities: []string{"aarch64"}},
				"build02": {Provider: "GCP", Capacity: 20, IPCapacity: 100, Capabilities: []string{"amd64"}},
			},
			expected: true,
		},
		{
			name: "No corresponding clusters in next map",
			prev: ClusterMap{
				"build01": {Provider: "AWS", Capacity: 10, Capabilities: []string{"aarch64", "intranet", "rce", "sshd-bastion"}},
				"build02": {Provider: "GCP", Capacity: 20, Capabilities: []string{"amd64", "intranet", "rce", "sshd-bastion"}},
			},
			next: ClusterMap{
				"build03": {Provider: "AWS", Capacity: 15, Capabilities: []string{"aarch64", "intranet", "rce", "sshd-bastion"}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := HasCapacityOrCapabilitiesChanged(tt.prev, tt.next)
			if diff := cmp.Diff(tt.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tt.name, diff)
			}
		})
	}
}

func TestLoadWeight(t *testing.T) {
	tests := []struct {
		name     string
		info     ClusterInfo
		maxIP    int
		expected float64
	}{
		{
			name:     "capacity only when no cluster sets ipCapacity",
			info:     ClusterInfo{Capacity: 100},
			maxIP:    0,
			expected: 100,
		},
		{
			name:     "omitted ipCapacity uses maxIP baseline",
			info:     ClusterInfo{Capacity: 100},
			maxIP:    743,
			expected: 100,
		},
		{
			name:     "configured ipCapacity scales against maxIP",
			info:     ClusterInfo{Capacity: 100, IPCapacity: 167},
			maxIP:    743,
			expected: 100 * 167.0 / 743.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := LoadWeight(tt.info, tt.maxIP)
			if diff := cmp.Diff(tt.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tt.name, diff)
			}
		})
	}
}
