package testhelper

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/util"
)

type FakePodExecutor struct {
	loggingclient.LoggingClient
	Failures    sets.Set[string]
	Pending     sets.Set[string] // Pods that should stay in Pending state
	CreatedPods []*coreapi.Pod
	DeletedPods []*coreapi.Pod
	lock        sync.Mutex
}

func (f *FakePodExecutor) Create(ctx context.Context, o ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if pod, ok := o.(*coreapi.Pod); ok {
		if pod.Namespace == "" {
			return errors.New("pod had no namespace set")
		}
		func() {
			f.lock.Lock()
			defer f.lock.Unlock()
			f.CreatedPods = append(f.CreatedPods, pod.DeepCopy())
		}()
		pod.Status.Phase = coreapi.PodPending
	}
	return f.LoggingClient.Create(ctx, o, opts...)
}

func (f *FakePodExecutor) Delete(ctx context.Context, o ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
	if pod, ok := o.(*coreapi.Pod); ok {
		func() {
			f.lock.Lock()
			defer f.lock.Unlock()
			f.DeletedPods = append(f.DeletedPods, pod.DeepCopy())
		}()
	}
	return f.LoggingClient.Delete(ctx, o, opts...)
}

func (f *FakePodExecutor) Get(ctx context.Context, n ctrlruntimeclient.ObjectKey, o ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	if err := f.LoggingClient.Get(ctx, n, o); err != nil {
		return err
	}
	if pod, ok := o.(*coreapi.Pod); ok {
		f.process(pod)
	}
	return nil
}

func (f *FakePodExecutor) Watch(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	if err := f.LoggingClient.List(ctx, list, opts...); err != nil {
		return nil, err
	}
	filter(list, opts...)
	items := list.(*coreapi.PodList).Items
	ch := make(chan watch.Event, len(items))
	for _, x := range items {
		f.process(&x)
		ch <- watch.Event{Type: watch.Modified, Object: &x}
	}
	return watch.NewProxyWatcher(ch), nil
}

func (f *FakePodExecutor) process(pod *coreapi.Pod) {
	// If the pod should stay pending, don't transition it
	if f.Pending != nil && f.Pending.Has(pod.Name) {
		pod.Status.Phase = coreapi.PodPending
		return
	}

	fail := f.Failures.Has(pod.Name)
	if fail {
		pod.Status.Phase = coreapi.PodFailed
	} else {
		pod.Status.Phase = coreapi.PodSucceeded
	}
	for _, container := range pod.Spec.Containers {
		terminated := &coreapi.ContainerStateTerminated{}
		if fail {
			terminated.ExitCode = 1
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, coreapi.ContainerStatus{
			Name:  container.Name,
			State: coreapi.ContainerState{Terminated: terminated}})
	}
}

// The fake client version we use (v0.12.3) does not implement field selectors.
func filter(list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) {
	var o ctrlruntimeclient.ListOptions
	for _, x := range opts {
		x.ApplyToList(&o)
	}
	items := &list.(*coreapi.PodList).Items
	*items = util.RemoveIf(*items, func(p coreapi.Pod) bool {
		return !o.FieldSelector.Matches(fields.Set{"metadata.name": p.Name})
	})
}

type FakePodClient struct {
	*FakePodExecutor
	Namespace, Name string
	PendingTimeout  time.Duration
}

func (f FakePodClient) GetPendingTimeout() time.Duration {
	return f.PendingTimeout
}

func (f *FakePodClient) Exec(namespace, name string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error) {
	if namespace != f.Namespace {
		return nil, fmt.Errorf("unexpected Namespace: %q", namespace)
	}
	if name != f.Name {
		return nil, fmt.Errorf("unexpected name: %q", name)
	}
	return &testExecutor{command: opts.Command}, nil
}

func (*FakePodClient) GetLogs(string, string, *coreapi.PodLogOptions) *rest.Request {
	return rest.NewRequestWithClient(nil, "", rest.ClientContentConfig{}, nil)
}

func (f *FakePodClient) WithNewLoggingClient() kubernetes.PodClient {
	return f
}

func (f *FakePodClient) MetricsAgent() *metrics.MetricsAgent {
	return nil
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

func (e testExecutor) StreamWithContext(ctx context.Context, opts remotecommand.StreamOptions) error {
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
