package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1 "github.com/openshift/api/config/v1"
)

func init() {
	if err := configv1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add configv1 scheme: %v", err))
	}
}

func TestMetricsAgent_RecordConfigurationInsight(t *testing.T) {
	tests := []struct {
		name            string
		targets         []string
		promote         bool
		org             string
		repo            string
		branch          string
		variant         string
		baseNamespace   string
		consoleHost     string
		nodeName        string
		clusterProfiles []ClusterProfileForTarget
		expectedEvents  []MetricsEvent
	}{
		{
			name:          "with cluster profiles",
			targets:       []string{"test1", "test2"},
			promote:       true,
			org:           "test-org",
			repo:          "test-repo",
			branch:        "main",
			variant:       "test-variant",
			baseNamespace: "test-namespace",
			consoleHost:   "console.example.com",
			nodeName:      "test-node",
			clusterProfiles: []ClusterProfileForTarget{
				{Target: "test1", ProfileName: "aws"},
				{Target: "test2", ProfileName: "gcp"},
			},
			expectedEvents: []MetricsEvent{
				&InsightsEvent{
					Name: string(InsightConfiguration),
					AdditionalContext: Context{
						"targets":        []string{"test1", "test2"},
						"promote":        true,
						"org":            "test-org",
						"repo":           "test-repo",
						"branch":         "main",
						"variant":        "test-variant",
						"base_namespace": "test-namespace",
						"cluster_info": Context{
							"console_host": "console.example.com",
							"node_name":    "test-node",
							"cluster_profiles": []ClusterProfileForTarget{
								{Target: "test1", ProfileName: "aws"},
								{Target: "test2", ProfileName: "gcp"},
							},
							"cluster_id": "test-cluster-id",
						},
					},
				},
			},
		},
		{
			name:            "without cluster profiles",
			targets:         []string{"test1"},
			promote:         false,
			org:             "test-org",
			repo:            "test-repo",
			branch:          "main",
			variant:         "test-variant",
			baseNamespace:   "test-namespace",
			consoleHost:     "console.example.com",
			nodeName:        "test-node",
			clusterProfiles: []ClusterProfileForTarget{},
			expectedEvents: []MetricsEvent{
				&InsightsEvent{
					Name: string(InsightConfiguration),
					AdditionalContext: Context{
						"targets":        []string{"test1"},
						"promote":        false,
						"org":            "test-org",
						"repo":           "test-repo",
						"branch":         "main",
						"variant":        "test-variant",
						"base_namespace": "test-namespace",
						"cluster_info": Context{
							"console_host":     "console.example.com",
							"node_name":        "test-node",
							"cluster_profiles": []ClusterProfileForTarget{},
							"cluster_id":       "test-cluster-id",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterVersion := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{ClusterID: "test-cluster-id"},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(clusterVersion).Build()
			l := logrus.WithField("test", tt.name)
			agent := &MetricsAgent{
				ctx:            context.Background(),
				events:         make(chan MetricsEvent, 100),
				client:         fakeClient,
				logger:         l,
				insightsPlugin: newInsightsPlugin(l),
			}

			go agent.Run()
			agent.RecordConfigurationInsight(tt.targets, tt.promote, tt.org, tt.repo, tt.branch, tt.variant, tt.baseNamespace, tt.consoleHost, tt.nodeName, tt.clusterProfiles)
			time.Sleep(100 * time.Millisecond)

			agent.mu.Lock()
			close(agent.events)
			agent.mu.Unlock()
			agent.wg.Wait()

			if diff := cmp.Diff(tt.expectedEvents, agent.insightsPlugin.Events(), cmpopts.IgnoreFields(InsightsEvent{}, "Timestamp")); diff != "" {
				t.Errorf("Events mismatch: %s", diff)
			}
		})
	}
}
