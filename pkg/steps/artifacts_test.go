package steps

import (
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
	testhelper_kube "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
	"github.com/openshift/ci-tools/pkg/util"
)

var testArtifactsContainer = coreapi.Container{
	Name:  "artifacts",
	Image: "quay.io/prometheus/busybox:latest",
	VolumeMounts: []coreapi.VolumeMount{
		{Name: "artifacts", MountPath: "/tmp/artifacts"},
	},
	Command: []string{
		"/bin/sh",
		"-c",
		`#!/bin/sh
set -euo pipefail
trap 'kill $(jobs -p); exit 0' TERM

touch /tmp/done
echo "Waiting for artifacts to be extracted"
while true; do
	if [[ ! -f /tmp/done ]]; then
		echo "Artifacts extracted, will terminate after 30s"
		sleep 30
		echo "Exiting"
		exit 0
	fi
	sleep 5 & wait
done
`,
	},
}

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
			name: "no completed containers",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Annotations: map[string]string{
						annotationContainersForSubTestResults: "test",
					},
				},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{Name: "test"},
						{Name: "other"},
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
							Name: "other",
						},
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
				{Name: "container test", FailureOutput: &junit.FailureOutput{Output: "exit message"}},
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
				{Name: "container other", FailureOutput: &junit.FailureOutput{Output: "exit message"}},
				{Name: "container test", FailureOutput: &junit.FailureOutput{Output: "exit message"}},
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
				{Name: "container other"},
				{Name: "container test", FailureOutput: &junit.FailureOutput{Output: "exit message"}},
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
				{Name: "container test", FailureOutput: &junit.FailureOutput{Output: "exit message"}},
			},
		},
		{
			name: "sets duration to non-overlapping timelines",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{Annotations: map[string]string{annotationContainersForSubTestResults: "other,test"}},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   1,
									Message:    "exit message",
									StartedAt:  meta.Time{Time: time.Unix(1000, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1100, 0)},
								},
							},
						},
						{
							Name: "other",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   0,
									Message:    "success",
									StartedAt:  meta.Time{Time: time.Unix(1050, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1150, 0)},
								},
							},
						},
					},
				},
			},
			wantTests: []*junit.TestCase{
				{Name: "container other", Duration: 50},
				{Name: "container test", FailureOutput: &junit.FailureOutput{Output: "exit message"}, Duration: 100},
			},
		},
		{
			name: "sets duration to non-overlapping timelines - reverse order",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{Annotations: map[string]string{annotationContainersForSubTestResults: "other,test"}},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "other",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   0,
									Message:    "success",
									StartedAt:  meta.Time{Time: time.Unix(1050, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1150, 0)},
								},
							},
						},
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   1,
									Message:    "exit message",
									StartedAt:  meta.Time{Time: time.Unix(1000, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1100, 0)},
								},
							},
						},
					},
				},
			},
			wantTests: []*junit.TestCase{
				{Name: "container other", Duration: 50},
				{Name: "container test", FailureOutput: &junit.FailureOutput{Output: "exit message"}, Duration: 100},
			},
		},
		{
			name: "handles non-overlapping container lifecycles",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{Annotations: map[string]string{annotationContainersForSubTestResults: "other,test"}},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "other",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   0,
									Message:    "success",
									StartedAt:  meta.Time{Time: time.Unix(1050, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1150, 0)},
								},
							},
						},
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   1,
									Message:    "exit message",
									StartedAt:  meta.Time{Time: time.Unix(1200, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1250, 0)},
								},
							},
						},
					},
				},
			},
			wantTests: []*junit.TestCase{
				{Name: "container other", Duration: 100},
				{Name: "container test", FailureOutput: &junit.FailureOutput{Output: "exit message"}, Duration: 50},
			},
		},
		{
			name: "handles fully overlapping times",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{Annotations: map[string]string{annotationContainersForSubTestResults: "other,test"}},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "other",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   0,
									Message:    "success",
									StartedAt:  meta.Time{Time: time.Unix(1050, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1150, 0)},
								},
							},
						},
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   1,
									Message:    "exit message",
									StartedAt:  meta.Time{Time: time.Unix(1100, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1150, 0)},
								},
							},
						},
					},
				},
			},
			wantTests: []*junit.TestCase{
				{Name: "container other", Duration: 100},
				{Name: "container test", FailureOutput: &junit.FailureOutput{Output: "exit message"}, Duration: 0},
			},
		},
		{
			name: "handles fully overlapping identical ",
			pod: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{Annotations: map[string]string{annotationContainersForSubTestResults: "other,test"}},
				Status: coreapi.PodStatus{
					ContainerStatuses: []coreapi.ContainerStatus{
						{
							Name: "other",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   0,
									Message:    "success",
									StartedAt:  meta.Time{Time: time.Unix(1000, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1100, 0)},
								},
							},
						},
						{
							Name: "test",
							State: coreapi.ContainerState{
								Terminated: &coreapi.ContainerStateTerminated{
									ExitCode:   1,
									Message:    "exit message",
									StartedAt:  meta.Time{Time: time.Unix(1100, 0)},
									FinishedAt: meta.Time{Time: time.Unix(1100, 0)},
								},
							},
						},
					},
				},
			},
			wantTests: []*junit.TestCase{
				{Name: "container other", Duration: 100},
				{Name: "container test", FailureOutput: &junit.FailureOutput{Output: "exit message"}, Duration: 0},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &TestCaseNotifier{
				nested:  util.NopNotifier,
				lastPod: tt.pod,
			}
			tests := n.SubTests(tt.prefix)
			if !reflect.DeepEqual(tt.wantTests, tests) {
				t.Fatalf("unexpected: %s", diff.ObjectReflectDiff(tt.wantTests, tests))
			}
		})
	}
}

func TestArtifactWorker(t *testing.T) {
	tmp, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			t.Errorf("couldn't clean up tmpdir: %v", err)
		}
	}()
	pod := "pod"
	podClient := &testhelper_kube.FakePodClient{
		FakePodExecutor: &testhelper_kube.FakePodExecutor{
			Lock: sync.RWMutex{},
			LoggingClient: loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&coreapi.Pod{
					ObjectMeta: meta.ObjectMeta{
						Name:      pod,
						Namespace: "namespace",
					},
					Status: coreapi.PodStatus{
						ContainerStatuses: []coreapi.ContainerStatus{
							{
								Name: "artifacts",
								State: coreapi.ContainerState{
									Running: &coreapi.ContainerStateRunning{},
								},
							},
						},
					},
				}).Build()),
		},
		Namespace: "namespace",
		Name:      pod,
	}
	w := NewArtifactWorker(podClient, tmp, "namespace")
	w.CollectFromPod(pod, []string{"container"}, nil)
	w.Complete(pod)
	select {
	case <-w.Done(pod):
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for artifact worker to finish")
	}
	files, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	if diff := cmp.Diff(names, []string{"test.txt"}); diff != "" {
		t.Fatalf("artifacts do not match expected: %s", diff)
	}
}

func TestAddArtifactsToPod(t *testing.T) {
	testCases := []struct {
		testID   string
		pod      *coreapi.Pod
		expected *coreapi.Pod
	}{
		{
			testID: "pod object has no artifacts volumes/volumeMounts, artifacts container injection is not expected",
			pod: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{Name: "test"}},
				},
			},
			expected: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{Name: "test"}},
				},
			},
		},
		{
			testID: "pod object has only volumes but no container is using it, injection is not expected",
			pod: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Volumes:    []coreapi.Volume{{Name: "artifacts"}},
					Containers: []coreapi.Container{{Name: "test"}},
				},
			},
			expected: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Volumes:    []coreapi.Volume{{Name: "artifacts"}},
					Containers: []coreapi.Container{{Name: "test"}},
				},
			},
		},
		{
			testID: "pod object has artifacts volumes/volumeMounts, artifacts container injection expected",
			pod: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Volumes: []coreapi.Volume{{Name: "artifacts"}},
					Containers: []coreapi.Container{
						{
							Name:         "test",
							VolumeMounts: []coreapi.VolumeMount{{Name: "artifacts"}},
						},
					},
				},
			},
			expected: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Volumes: []coreapi.Volume{{Name: "artifacts"}},
					Containers: []coreapi.Container{
						{
							Name:         "test",
							VolumeMounts: []coreapi.VolumeMount{{Name: "artifacts"}},
						},
						testArtifactsContainer,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			addArtifactsToPod(tc.pod)
			if !equality.Semantic.DeepEqual(tc.pod, tc.expected) {
				t.Fatal(diff.ObjectReflectDiff(tc.pod, tc.expected))
			}

		})
	}
}

func TestArtifactsContainer(t *testing.T) {
	artifacts := artifactsContainer()
	if !reflect.DeepEqual(artifacts, testArtifactsContainer) {
		t.Fatal(diff.ObjectReflectDiff(artifacts, testArtifactsContainer))
	}
}

func TestAddPodUtils(t *testing.T) {
	base := &coreapi.Pod{
		TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
		Spec: coreapi.PodSpec{
			Containers: []coreapi.Container{
				{
					Name:    "test",
					Command: []string{"cmd"},
					Args:    []string{"arg1", "arg2"},
				},
			},
		},
	}
	if err := addPodUtils(base, "mydir", &prowv1.DecorationConfig{
		Timeout:     &prowv1.Duration{Duration: 4 * time.Hour},
		GracePeriod: &prowv1.Duration{Duration: 30 * time.Minute},
		UtilityImages: &prowv1.UtilityImages{
			Entrypoint: "entrypoint",
			Sidecar:    "sidecar",
		},
		GCSConfiguration: &prowv1.GCSConfiguration{
			Bucket:       "bucket",
			PathStrategy: prowv1.PathStrategyExplicit,
		},
		GCSCredentialsSecret: func() *string { s := "gce-sa-credentials-gcs-publisher"; return &s }(),
	}, "rawspec", []coreapi.VolumeMount{{Name: "secret", MountPath: "/secret"}},
		&GeneratePodOptions{Clone: false, PropagateExitCode: true}, nil); err != nil {
		t.Errorf("failed to decorate: %v", err)
	}
	testhelper.CompareWithFixture(t, base)
}
