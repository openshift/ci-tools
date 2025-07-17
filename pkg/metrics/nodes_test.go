package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNodesMetricsPlugin_AddNodeForWorkload(t *testing.T) {
	testCases := []struct {
		name          string
		nodeWorkloads map[string]sets.Set[string]
		nodeName      string
		workloadName  string
		expectedState map[string]sets.Set[string]
	}{
		{
			name:          "add workload to new node",
			nodeName:      "node-1",
			workloadName:  "workload-1",
			nodeWorkloads: map[string]sets.Set[string]{},
			expectedState: map[string]sets.Set[string]{"node-1": sets.New("workload-1")},
		},
		{
			name:          "add workload to existing node",
			nodeName:      "node-1",
			workloadName:  "workload-2",
			nodeWorkloads: map[string]sets.Set[string]{"node-1": sets.New("workload-1")},
			expectedState: map[string]sets.Set[string]{"node-1": sets.New("workload-1", "workload-2")},
		},
		{
			name:          "add duplicate workload to node",
			nodeName:      "node-1",
			workloadName:  "workload-1",
			nodeWorkloads: map[string]sets.Set[string]{"node-1": sets.New("workload-1")},
			expectedState: map[string]sets.Set[string]{"node-1": sets.New("workload-1")},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p := &nodesMetricsPlugin{
				logger:        logrus.WithField("test", tc.name),
				nodeWorkloads: tc.nodeWorkloads,
				nodesCh:       make(chan string, 10),
				watchTimes:    make(map[string]time.Time),
			}

			p.AddNodeForWorkload(tc.nodeName, tc.workloadName)
			if diff := cmp.Diff(tc.expectedState, p.nodeWorkloads); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestNodesMetricsPlugin_RemoveWorkload(t *testing.T) {
	testCases := []struct {
		name          string
		nodeWorkloads map[string]sets.Set[string]
		workloadName  string
		expectedState map[string]sets.Set[string]
	}{
		{
			name:          "remove workload from node",
			nodeWorkloads: map[string]sets.Set[string]{"node-1": sets.New("workload-1", "workload-2")},
			workloadName:  "workload-1",
			expectedState: map[string]sets.Set[string]{"node-1": sets.New("workload-2")},
		},
		{
			name:          "remove last workload from node",
			nodeWorkloads: map[string]sets.Set[string]{"node-1": sets.New("workload-1"), "node-2": sets.New("workload-2")},
			workloadName:  "workload-1",
			expectedState: map[string]sets.Set[string]{"node-2": sets.New("workload-2")},
		},
		{
			name:          "remove workload that doesn't exist",
			nodeWorkloads: map[string]sets.Set[string]{"node-1": sets.New("workload-1")},
			workloadName:  "workload-2",
			expectedState: map[string]sets.Set[string]{"node-1": sets.New("workload-1")},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p := &nodesMetricsPlugin{
				nodeWorkloads: tc.nodeWorkloads,
				logger:        logrus.WithField("test", tc.name),
			}
			p.RemoveWorkload(tc.workloadName)
			if diff := cmp.Diff(tc.expectedState, p.nodeWorkloads); diff != "" {
				t.Fatal(diff)
			}

		})
	}
}

func Test_calculateStats(t *testing.T) {
	tests := []struct {
		name    string
		data    []int
		wantMin int
		wantMax int
		wantAvg int
	}{
		{
			name:    "empty data",
			data:    []int{},
			wantMin: 0,
			wantMax: 0,
			wantAvg: 0,
		},
		{
			name:    "single value",
			data:    []int{42},
			wantMin: 42,
			wantMax: 42,
			wantAvg: 42,
		},
		{
			name:    "multiple values",
			data:    []int{10, 20, 30},
			wantMin: 10,
			wantMax: 30,
			wantAvg: 20,
		},
		{
			name:    "all same values",
			data:    []int{15, 15, 15, 15},
			wantMin: 15,
			wantMax: 15,
			wantAvg: 15,
		},
		{
			name:    "with zero values",
			data:    []int{0, 5, 10},
			wantMin: 0,
			wantMax: 10,
			wantAvg: 5,
		},
		{
			name:    "negative values",
			data:    []int{-10, -5, 0, 5, 10},
			wantMin: -10,
			wantMax: 10,
			wantAvg: 0,
		},
		{
			name:    "large dataset",
			data:    []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			wantMin: 1,
			wantMax: 10,
			wantAvg: 5,
		},
		{
			name:    "unordered values",
			data:    []int{50, 10, 30, 20, 40},
			wantMin: 10,
			wantMax: 50,
			wantAvg: 30,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMin, gotMax, gotAvg := calculateStats(tt.data)
			if gotMin != tt.wantMin {
				t.Errorf("calculateStats() gotMin = %v, want %v", gotMin, tt.wantMin)
			}
			if gotMax != tt.wantMax {
				t.Errorf("calculateStats() gotMax = %v, want %v", gotMax, tt.wantMax)
			}
			if gotAvg != tt.wantAvg {
				t.Errorf("calculateStats() gotAvg = %v, want %v", gotAvg, tt.wantAvg)
			}
		})
	}
}

func TestNodesMetricsPlugin_pollNode(t *testing.T) {
	testCases := []struct {
		name            string
		node            *corev1.Node
		clientError     bool
		expectedCPU     int
		expectedMemory  int
		expectedStorage int
	}{
		{
			name:            "client error returns zeros",
			node:            nil,
			clientError:     true,
			expectedCPU:     0,
			expectedMemory:  0,
			expectedStorage: 0,
		},
		{
			name: "zero capacity returns zeros",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("0"),
						corev1.ResourceMemory:           resource.MustParse("0"),
						corev1.ResourceEphemeralStorage: resource.MustParse("0"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("0"),
						corev1.ResourceMemory:           resource.MustParse("0"),
						corev1.ResourceEphemeralStorage: resource.MustParse("0"),
					},
				},
			},
			expectedCPU:     0,
			expectedMemory:  0,
			expectedStorage: 0,
		},
		{
			name: "full capacity available",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("4000m"),
						corev1.ResourceMemory:           resource.MustParse("8Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("4000m"),
						corev1.ResourceMemory:           resource.MustParse("8Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
					},
				},
			},
			expectedCPU:     0,
			expectedMemory:  0,
			expectedStorage: 0,
		},
		{
			name: "some resources reserved",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("4000m"),
						corev1.ResourceMemory:           resource.MustParse("8Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("3000m"),
						corev1.ResourceMemory:           resource.MustParse("6Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("80Gi"),
					},
				},
			},
			expectedCPU:     25,
			expectedMemory:  25,
			expectedStorage: 20,
		},
		{
			name: "high reservation",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("2000m"),
						corev1.ResourceMemory:           resource.MustParse("4Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("50Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("500m"),
						corev1.ResourceMemory:           resource.MustParse("1Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("10Gi"),
					},
				},
			},
			expectedCPU:     75,
			expectedMemory:  75,
			expectedStorage: 80,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := fakectrlruntimeclient.NewClientBuilder().Build()
			if !tc.clientError {
				client = fakectrlruntimeclient.NewClientBuilder().WithObjects(tc.node).Build()
			}

			p := &nodesMetricsPlugin{
				client: client,
				logger: logrus.NewEntry(logrus.New()),
			}

			cpuUtil, memUtil, storageUtil := p.pollNode(context.Background(), "test-node")

			if cpuUtil != tc.expectedCPU {
				t.Errorf("Expected CPU utilization %d%%, got %d%%", tc.expectedCPU, cpuUtil)
			}
			if memUtil != tc.expectedMemory {
				t.Errorf("Expected memory utilization %d%%, got %d%%", tc.expectedMemory, memUtil)
			}
			if storageUtil != tc.expectedStorage {
				t.Errorf("Expected storage utilization %d%%, got %d%%", tc.expectedStorage, storageUtil)
			}
		})
	}
}

func TestNodesMetricsPlugin_createAndStoreNodeEventWithWorkloads(t *testing.T) {
	testCases := []struct {
		name                   string
		nodeName               string
		workloads              []string
		cpuUtilization         []int
		memUtilization         []int
		storageUtilization     []int
		expectedEventCount     int
		expectedWorkloads      []string
		expectedCPUReservation SystemReservationInfo
	}{
		{
			name:               "creates event with workloads and stats",
			nodeName:           "test-node",
			workloads:          []string{"workload-1", "workload-2"},
			cpuUtilization:     []int{10, 20, 30},
			memUtilization:     []int{5, 15, 25},
			storageUtilization: []int{8, 12, 16},
			expectedEventCount: 1,
			expectedWorkloads:  []string{"workload-1", "workload-2"},
			expectedCPUReservation: SystemReservationInfo{
				MinCPUPercent: 10,
				MaxCPUPercent: 30,
				AvgCPUPercent: 20,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := fakectrlruntimeclient.NewClientBuilder().WithObjects(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: tc.nodeName},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4000m"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			}).Build()

			p := &nodesMetricsPlugin{
				client:        client,
				nodeWorkloads: make(map[string]sets.Set[string]),
				watchTimes:    make(map[string]time.Time),
				events:        []NodeEvent{},
				logger:        logrus.NewEntry(logrus.New()),
			}

			pollStartTime := time.Now()
			p.createAndStoreNodeEventWithWorkloads(tc.nodeName, pollStartTime, tc.cpuUtilization, tc.memUtilization, tc.storageUtilization, tc.workloads)

			if len(p.events) != tc.expectedEventCount {
				t.Errorf("Expected %d events, got %d", tc.expectedEventCount, len(p.events))
			}

			if len(p.events) > 0 {
				event := p.events[0]
				if event.Node != tc.nodeName {
					t.Errorf("Expected node %s, got %s", tc.nodeName, event.Node)
				}
				if diff := cmp.Diff(tc.expectedWorkloads, event.Workloads); diff != "" {
					t.Errorf("Workloads mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
