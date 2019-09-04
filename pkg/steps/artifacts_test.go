package steps

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/openshift/ci-tools/pkg/junit"
	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/fake"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/remotecommand"
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

type testPodClient struct {
	coreclientset.PodsGetter
	namespace, name string
}

func (c testPodClient) Exec(namespace, name string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error) {
	if namespace != c.namespace {
		return nil, fmt.Errorf("unexpected namespace: %q", namespace)
	}
	if name != c.name {
		return nil, fmt.Errorf("unexpected name: %q", name)
	}
	return &testExecutor{command: opts.Command}, nil
}

type testExecutor struct {
	command []string
}

func (e testExecutor) Stream(opts remotecommand.StreamOptions) error {
	if reflect.DeepEqual(e.command, []string{"tar", "czf", "-", "-C", "/tmp/artifacts", "."}) {
		var tar []byte
		tar, err := base64.StdEncoding.DecodeString(`
H4sIAMq1b10AA+3RPQrDMAyGYc09hU8QrCpOzuOAKR2y2Ar0+HX/tnboEErhfRbxoW8QyEvzwS8uO4r
dNI63qXOK96yP/JRELZnNdpySSlTrBQlxz6Netua5hiDLctrOa665tA+9Ut9v/pr3/x9+fQQAAAAAAA
AAAAAAAAAA4GtXigWTnQAoAAA=`)
		if err != nil {
			return err
		}
		_, err = opts.Stdout.Write(tar)
		return err
	} else if reflect.DeepEqual(e.command, []string{"rm", "-f", "/tmp/done"}) {
		return nil
	}
	return fmt.Errorf("unexpected command: %v", e.command)
}

func TestArtifactWorker(t *testing.T) {
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			t.Errorf("couldn't clean up tmpdir: %v", err)
		}
	}()
	pod := "pod"
	podClient := testPodClient{
		PodsGetter: fake.NewSimpleClientset().CoreV1(),
		namespace:  "namespace",
		name:       pod,
	}
	w := NewArtifactWorker(podClient, tmp, podClient.namespace)
	w.CollectFromPod(pod, true, []string{"container"}, nil)
	w.Complete(pod)
	for !w.Done(pod) {
	}
	files, err := ioutil.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	if !reflect.DeepEqual(names, []string{"test.txt"}) {
		t.Fatalf("unexpected content in the artifact directory: %v", names)
	}
}
