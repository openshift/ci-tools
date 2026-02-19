package metrics

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	autoscalingv1beta1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1beta1"
)

func init() {
	_ = machinev1beta1.AddToScheme(scheme.Scheme)
	_ = autoscalingv1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
}

func TestMachinesPlugin_Record(t *testing.T) {
	testCases := []struct {
		name        string
		objects     []ctrlruntimeclient.Object
		autoscalers []autoscalingv1beta1.MachineAutoscaler
		event       *MachinesEvent
		expected    []MetricsEvent
	}{
		{
			name: "machines event with workload",
			autoscalers: []autoscalingv1beta1.MachineAutoscaler{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "tests-amd64-us-east-1a-autoscaler"},
					Spec:       autoscalingv1beta1.MachineAutoscalerSpec{MinReplicas: 0, MaxReplicas: 40, ScaleTargetRef: autoscalingv1beta1.CrossVersionObjectReference{Name: "tests-amd64-us-east-1a"}},
				},
			},
			objects: []ctrlruntimeclient.Object{
				&machinev1beta1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tests-amd64-us-east-1a",
						Namespace: MachineAPINamespace,
					},
					Spec: machinev1beta1.MachineSetSpec{
						Template: machinev1beta1.MachineTemplateSpec{
							Spec: machinev1beta1.MachineSpec{
								ObjectMeta: machinev1beta1.ObjectMeta{
									Labels: map[string]string{CIWorkloadLabel: "tests"},
								},
							},
						},
					},
					Status: machinev1beta1.MachineSetStatus{Replicas: 3},
				},
				&machinev1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tests-machine-1",
						Namespace: MachineAPINamespace,
						Labels:    map[string]string{MachineSetLabel: "tests-amd64-us-east-1a"},
					},
					Status: machinev1beta1.MachineStatus{Phase: stringPtr("Running")},
				},
				&machinev1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tests-machine-2",
						Namespace: MachineAPINamespace,
						Labels:    map[string]string{MachineSetLabel: "tests-amd64-us-east-1a"},
					},
					Status: machinev1beta1.MachineStatus{Phase: stringPtr("Running")},
				},
				&machinev1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tests-machine-3",
						Namespace: MachineAPINamespace,
						Labels:    map[string]string{MachineSetLabel: "tests-amd64-us-east-1a"},
					},
					Status: machinev1beta1.MachineStatus{Phase: stringPtr("Provisioning")},
				},
			},
			event: &MachinesEvent{
				Type:      PodCreation,
				PodName:   "test-pod",
				Namespace: "test-ns",
				Workload:  "tests",
			},
			expected: []MetricsEvent{
				&MachinesEvent{
					Type:      PodCreation,
					PodName:   "test-pod",
					Namespace: "test-ns",
					Workload:  "tests",
					WorkloadCapacity: WorkloadNodeCount{
						Workload:      "tests",
						TotalMachines: 3,
						MachineSets: []MachineSetCount{
							{
								Name:       "tests-amd64-us-east-1a",
								Current:    3,
								Autoscaler: &AutoscalerInfo{Name: "tests-amd64-us-east-1a-autoscaler", Min: 0, Max: 40},
								Machines: []MachineInfo{
									{Name: "tests-machine-1", Phase: "Running"},
									{Name: "tests-machine-2", Phase: "Running"},
									{Name: "tests-machine-3", Phase: "Provisioning"},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := NewMachinesPlugin(
				context.Background(),
				logrus.WithField("test", tc.name),
				fake.NewClientBuilder().WithObjects(tc.objects...).Build(),
				tc.autoscalers,
			)

			plugin.Record(tc.event)

			events := plugin.Events()
			if diff := cmp.Diff(tc.expected, events); diff != "" {
				t.Errorf("unexpected events (-want +got):\n%s", diff)
			}
		})
	}
}
