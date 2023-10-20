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
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/util"
)

// PodRunnerEnv is a common environment that could be shared among multiple PodPayloadRunners,
// useful to make them communicate
type PodRunnerEnv struct {
	// TODO: so far its only purpose is about waiting for an observer to begin the execution: modify/extend when
	// new needs arise
	ObserverDone chan struct{}
}

func NewPodRunnerEnv() *PodRunnerEnv {
	return &PodRunnerEnv{
		ObserverDone: make(chan struct{}),
	}
}

// PodPayload represents the actual work a pod has to perform
type PodPayload func(pod *coreapi.Pod, env *PodRunnerEnv, dispatch func(events ...watch.Event))

// PodPayloadRunner is in charge of running a payload for a given pod
type PodPayloadRunner struct {
	env *PodRunnerEnv

	// Whether the pod is already running the payload
	running bool

	// Watchers are being used both by PodPayloadRunner and PodPayload, which can also be an async function
	watchersL sync.Locker
	// The payload could change the pod state during its execution and eventually inform
	// any watchers
	watchers []chan<- watch.Event

	payload PodPayload
}

func (r *PodPayloadRunner) AddWatcher(watcher chan<- watch.Event) {
	r.watchersL.Lock()
	defer r.watchersL.Unlock()
	r.watchers = append(r.watchers, watcher)
}

func (r *PodPayloadRunner) Run(pod *coreapi.Pod) {
	// Assume payload runs once
	if r.running {
		return
	}
	r.running = true

	// The payload could be an async function, it is advisable to make watchers thread safe
	dispatch := func(events ...watch.Event) {
		r.watchersL.Lock()
		defer r.watchersL.Unlock()
		for _, e := range events {
			for _, w := range r.watchers {
				w <- e
			}
		}
	}

	r.payload(pod, r.env, dispatch)
}

func NewPodPayloadRunner(payload PodPayload, env PodRunnerEnv) *PodPayloadRunner {
	return &PodPayloadRunner{
		env:       &env,
		watchersL: &sync.Mutex{},
		watchers:  make([]chan<- watch.Event, 0),
		payload:   payload,
	}
}

type FakePodExecutor struct {
	Lock sync.RWMutex
	loggingclient.LoggingClient
	Failures          sets.Set[string]
	CreatedPods       []*coreapi.Pod
	PodPayloadRunners map[string]*PodPayloadRunner
}

func (f *FakePodExecutor) Create(ctx context.Context, o ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	f.Lock.Lock()
	defer f.Lock.Unlock()
	if pod, ok := o.(*coreapi.Pod); ok {
		if pod.Namespace == "" {
			return errors.New("pod had no namespace set")
		}
		f.CreatedPods = append(f.CreatedPods, pod.DeepCopy())
		pod.Status.Phase = coreapi.PodPending
	}
	return f.LoggingClient.Create(ctx, o, opts...)
}

func (f *FakePodExecutor) Get(ctx context.Context, n ctrlruntimeclient.ObjectKey, o ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	f.Lock.RLock()
	defer f.Lock.RUnlock()
	if err := f.LoggingClient.Get(ctx, n, o); err != nil {
		return err
	}
	if pod, ok := o.(*coreapi.Pod); ok {
		f.process(pod, nil)
	}
	return nil
}

func (f *FakePodExecutor) Watch(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	f.Lock.RLock()
	defer f.Lock.RUnlock()
	if err := f.LoggingClient.List(ctx, list, opts...); err != nil {
		return nil, err
	}
	filter(list, opts...)
	items := list.(*coreapi.PodList).Items
	ch := make(chan watch.Event, len(items))
	for _, x := range items {
		if f.process(&x, ch) {
			ch <- watch.Event{Type: watch.Modified, Object: &x}
		}
	}
	return watch.NewProxyWatcher(ch), nil
}

// Process the pod and returns whether an event saying the pod has just been modified
// has to be dispatched or not
func (f *FakePodExecutor) process(pod *coreapi.Pod, ch chan watch.Event) bool {
	// If set, let the payload running to process the pod
	if payloadRunner, ok := f.PodPayloadRunners[pod.Name]; ok {
		if ch != nil {
			payloadRunner.AddWatcher(ch)
		}
		payloadRunner.Run(pod)
		// Do not dispatch any events here, let the PodPayload handle it
		return false
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
	return true
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
