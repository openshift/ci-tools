package steps

import (
	"reflect"
	"testing"

	"github.com/openshift/ci-operator/pkg/junit"
	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestTestCaseNotifier_SubTests(t *testing.T) {
	tests := []struct {
		name      string
		pod       *coreapi.Pod
		prefix    string
		wantTests []*junit.TestCase
	}{
		{name: "nil"},
		{
			name: "no annotation",
			pod: &coreapi.Pod{
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 1,
									Message:  "exit message",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "empty annotation",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Annotations: map[string]string{
						annotationContainersForSubTestResults: "",
					},
				},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 1,
									Message:  "exit message",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "annotation is invalid",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Annotations: map[string]string{
						annotationContainersForSubTestResults: ",",
					},
				},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 1,
									Message:  "exit message",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "annotation points to missing container",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Annotations: map[string]string{
						annotationContainersForSubTestResults: "other",
					},
				},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 1,
									Message:  "exit message",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "single failed container",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Annotations: map[string]string{
						annotationContainersForSubTestResults: "test",
					},
				},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 1,
									Message:  "exit message",
								},
							},
						},
					},
				},
			},
			wantTests: []*junit.TestCase{
				{
					Name: "container test",
					FailureOutput: &junit.FailureOutput{
						Output: "exit message",
					},
				},
			},
		},
		{
			name: "two failed containers, order is status",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Annotations: map[string]string{
						annotationContainersForSubTestResults: "other,test",
					},
				},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 1,
									Message:  "exit message",
								},
							},
						},
						{
							Name: "other",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 1,
									Message:  "exit message",
								},
							},
						},
					},
				},
			},
			wantTests: []*junit.TestCase{
				{
					Name: "container test",
					FailureOutput: &junit.FailureOutput{
						Output: "exit message",
					},
				},
				{
					Name: "container other",
					FailureOutput: &junit.FailureOutput{
						Output: "exit message",
					},
				},
			},
		},
		{
			name: "one failed, one succeeded",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Annotations: map[string]string{
						annotationContainersForSubTestResults: "other,test",
					},
				},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 1,
									Message:  "exit message",
								},
							},
						},
						{
							Name: "other",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 0,
									Message:  "success",
								},
							},
						},
					},
				},
			},
			wantTests: []*junit.TestCase{
				{
					Name: "container test",
					FailureOutput: &junit.FailureOutput{
						Output: "exit message",
					},
				},
				{
					Name: "container other",
				},
			},
		},
		{
			name: "ignores unfinisted container",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Annotations: map[string]string{
						annotationContainersForSubTestResults: "other,test",
					},
				},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode: 1,
									Message:  "exit message",
								},
							},
						},
						{
							Name:  "other",
							State: coreapi.ContainerState{},
						},
					},
				},
			},
			wantTests: []*junit.TestCase{
				{
					Name: "container test",
					FailureOutput: &junit.FailureOutput{
						Output: "exit message",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &TestCaseNotifier{
				nested:  nopNotifier{},
				lastPod: tt.pod,
			}
			tests := n.SubTests(tt.prefix)
			if !reflect.DeepEqual(tt.wantTests, tests) {
				t.Fatalf("unexpected: %s", diff.ObjectReflectDiff(tt.wantTests, tests))
			}
		})
	}
}
