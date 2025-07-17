package metrics

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgotesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	fakemetricsclient "k8s.io/metrics/pkg/client/clientset/versioned/fake"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
}

func convertMapToSyncMap(m map[string]sets.Set[string]) *sync.Map {
	sm := &sync.Map{}
	for k, v := range m {
		sm.Store(k, v)
	}
	return sm
}

func extractWorkloads(m *sync.Map) map[string]sets.Set[string] {
	out := make(map[string]sets.Set[string])
	m.Range(func(key, value any) bool {
		out[key.(string)] = value.(sets.Set[string])
		return true
	})
	return out
}

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
				nodeWorkloads: convertMapToSyncMap(tc.nodeWorkloads),
				nodesCh:       make(chan string, 10),
				watchTimes:    &sync.Map{},
			}

			p.AddNodeForWorkload(tc.nodeName, tc.workloadName)
			if diff := cmp.Diff(tc.expectedState, extractWorkloads(p.nodeWorkloads)); diff != "" {
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
				nodeWorkloads: convertMapToSyncMap(tc.nodeWorkloads),
				logger:        logrus.WithField("test", tc.name),
			}
			p.RemoveWorkload(tc.workloadName)
			if diff := cmp.Diff(tc.expectedState, extractWorkloads(p.nodeWorkloads)); diff != "" {
				t.Fatal(diff)
			}

		})
	}
}

func Test_calculateStats(t *testing.T) {
	tests := []struct {
		name    string
		data    []int64
		wantMin int64
		wantMax int64
		wantAvg int64
	}{
		{
			name:    "empty data",
			data:    []int64{},
			wantMin: 0,
			wantMax: 0,
			wantAvg: 0,
		},
		{
			name:    "single value",
			data:    []int64{42},
			wantMin: 42,
			wantMax: 42,
			wantAvg: 42,
		},
		{
			name:    "multiple values",
			data:    []int64{10, 20, 30},
			wantMin: 10,
			wantMax: 30,
			wantAvg: 20,
		},
		{
			name:    "all same values",
			data:    []int64{15, 15, 15, 15},
			wantMin: 15,
			wantMax: 15,
			wantAvg: 15,
		},
		{
			name:    "with zero values",
			data:    []int64{0, 5, 10},
			wantMin: 0,
			wantMax: 10,
			wantAvg: 5,
		},
		{
			name:    "negative values",
			data:    []int64{-10, -5, 0, 5, 10},
			wantMin: -10,
			wantMax: 10,
			wantAvg: 0,
		},
		{
			name:    "large dataset",
			data:    []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			wantMin: 1,
			wantMax: 10,
			wantAvg: 5,
		},
		{
			name:    "unordered values",
			data:    []int64{50, 10, 30, 20, 40},
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

func TestNodesMetricsPlugin_pollNodeMetrics(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := metricsv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add NodeMetrics to scheme: %v", err)
	}
	testCases := []struct {
		name           string
		nodeName       string
		metrics        *metricsv1beta1.NodeMetrics
		expectedCPU    int64
		expectedMemory int64
	}{
		{
			name:     "valid metrics",
			nodeName: "test-node",
			metrics: &metricsv1beta1.NodeMetrics{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1beta1", Kind: "NodeMetrices"},
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("800m"),
					corev1.ResourceMemory: resource.MustParse("2048Mi"),
				},
			},
			expectedCPU:    800,
			expectedMemory: 2048 * 1024 * 1024,
		},
		{
			name:           "no metrics available",
			nodeName:       "test-node",
			metrics:        nil,
			expectedCPU:    0,
			expectedMemory: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fakemetricsclient.NewSimpleClientset()

			fakeClient.Fake.PrependReactor("get", "nodes", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
				getAction, ok := action.(clientgotesting.GetAction)
				if !ok {
					return false, nil, nil
				}
				if getAction.GetName() == tc.nodeName {
					if tc.metrics != nil {
						return true, tc.metrics, nil
					}
				}
				return true, nil, kerrors.NewNotFound(action.GetResource().GroupResource(), getAction.GetName())
			})

			if tc.metrics != nil {
				err := fakeClient.Tracker().Add(tc.metrics)
				if err != nil {
					t.Fatalf("Failed to add NodeMetrics to fake client: %v", err)
				}
			}

			p := &nodesMetricsPlugin{
				metricsClient: fakeClient,
				logger:        logrus.WithField("test-name", tc.name),
			}

			cpuUsage, memUsage := p.pollNodeMetrics(context.Background(), tc.nodeName)
			if cpuUsage != tc.expectedCPU {
				t.Errorf("Expected CPU usage %d, got %d", tc.expectedCPU, cpuUsage)
			}
			if memUsage != tc.expectedMemory {
				t.Errorf("Expected memory usage %d, got %d", tc.expectedMemory, memUsage)
			}
		})
	}
}

func TestNodesMetricsPlugin_createAndStoreNodeEventWithWorkloads(t *testing.T) {
	testCases := []struct {
		name           string
		nodeName       string
		workloads      []string
		cpuUtilization []int64
		memUtilization []int64
		node           *corev1.Node
		expectedEvents []NodeEvent
	}{
		{
			name:           "creates event with workloads and stats",
			nodeName:       "test-node",
			workloads:      []string{"workload-1", "workload-2"},
			cpuUtilization: []int64{100, 200, 300},
			memUtilization: []int64{512000000, 1024000000, 2048000000},
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("4000m"),
						corev1.ResourceMemory:           resource.MustParse("8Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
						corev1.ResourcePods:             resource.MustParse("110"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("3500m"),
						corev1.ResourceMemory:           resource.MustParse("7Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("90Gi"),
						corev1.ResourcePods:             resource.MustParse("100"),
					},
				},
			},
			expectedEvents: []NodeEvent{
				{
					Node:      "test-node",
					Workloads: []string{"workload-1", "workload-2"},
					UsageStats: ResourceUsageStats{
						MinCPU: 100,
						MaxCPU: 300,
						AvgCPU: 200,
						MinMem: 512000000,
						MaxMem: 2048000000,
						AvgMem: 1194666666,
					},
					AgeSeconds: 9223372036,
					Resources: ResourcesInfo{
						Capacity:    ResourceDetails{CPU: "4", Memory: "8Gi", EphemeralStorage: "100Gi", Pods: "110"},
						Allocatable: ResourceDetails{CPU: "3500m", Memory: "7Gi", EphemeralStorage: "90Gi", Pods: "100"},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p := &nodesMetricsPlugin{
				client:     fakectrlruntimeclient.NewClientBuilder().WithObjects(tc.node).Build(),
				watchTimes: &sync.Map{},
				events:     []NodeEvent{},
				logger:     logrus.WithField("test-name", tc.name),
			}

			pollStartTime := time.Now()
			p.createAndStoreNodeEventWithWorkloads(tc.nodeName, pollStartTime, tc.cpuUtilization, tc.memUtilization, tc.workloads)

			if diff := cmp.Diff(tc.expectedEvents, p.events, cmpopts.IgnoreFields(NodeEvent{}, "Timestamp", "PollStarted", "WatchHistory")); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
