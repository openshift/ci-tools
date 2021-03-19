package steps

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/remotecommand"
	toolswatch "k8s.io/client-go/tools/watch"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/decorate"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
)

const (
	// A comma-delimited list of containers to wait for artifacts from within a pod. If not
	// specify, only 'artifacts' is waited for.
	annotationWaitForContainerArtifacts = "ci-operator.openshift.io/wait-for-container-artifacts"
	// A comma-delimited list of container names that will be returned as individual JUnit
	// test results.
	annotationContainersForSubTestResults = "ci-operator.openshift.io/container-sub-tests"
	// A boolean value which indicates that the logs from all containers in the
	// pod must be copied to the artifact directory (default is "false").
	annotationSaveContainerLogs = "ci-operator.openshift.io/save-container-logs"
	// artifactEnv is the env var in which we hold the artifact dir for users
	artifactEnv = "ARTIFACT_DIR"
)

// ContainerNotifier receives updates about the status of a poll action on a pod. The caller
// is required to define what notifications are made.
type ContainerNotifier interface {
	// Notify indicates that the provided container name has transitioned to an appropriate state and
	// any per container actions should be taken.
	Notify(pod *coreapi.Pod, containerName string)
	// Complete indicates the specified pod has completed execution, been deleted, or that no further
	// Notify() calls can be made.
	Complete(podName string)
	// Done returns a channel that can be used to wait for the specified pod name to complete the work it has pending.
	Done(podName string) <-chan struct{}
}

// NopNotifier takes no action when notified.
var NopNotifier = nopNotifier{}

type nopNotifier struct{}

func (nopNotifier) Notify(_ *coreapi.Pod, _ string) {}
func (nopNotifier) Complete(_ string)               {}
func (nopNotifier) Done(string) <-chan struct{} {
	ret := make(chan struct{})
	close(ret)
	return ret
}

// TestCaseNotifier allows a caller to generate per container JUnit test
// reports that provide better granularity for debugging problems when
// running tests in multi-container pods. It intercepts notifications and
// remembers the last pod retrieved which SubTests() will read.
//
// TestCaseNotifier must be called from a single thread.
type TestCaseNotifier struct {
	nested  ContainerNotifier
	lastPod *coreapi.Pod
}

// NewTestCaseNotifier wraps the provided ContainerNotifier and will
// create JUnit TestCase records for each container in the most recent
// pod to have completed.
func NewTestCaseNotifier(nested ContainerNotifier) *TestCaseNotifier {
	return &TestCaseNotifier{nested: nested}
}

func (n *TestCaseNotifier) Notify(pod *coreapi.Pod, containerName string) {
	n.nested.Notify(pod, containerName)
	n.lastPod = pod
}

func (n *TestCaseNotifier) Complete(podName string)             { n.nested.Complete(podName) }
func (n *TestCaseNotifier) Done(podName string) <-chan struct{} { return n.nested.Done(podName) }

// SubTests returns one junit test for each terminated container with a name
// in the annotation 'ci-operator.openshift.io/container-sub-tests' in the pod.
// Invoking SubTests clears the last pod, so subsequent calls will return no
// tests unless Notify() has been called in the meantime.
func (n *TestCaseNotifier) SubTests(prefix string) []*junit.TestCase {
	if n.lastPod == nil {
		return nil
	}
	pod := n.lastPod
	n.lastPod = nil

	names := sets.NewString(strings.Split(pod.Annotations[annotationContainersForSubTestResults], ",")...)
	names.Delete("")
	if len(names) == 0 {
		return nil
	}
	statuses := make([]coreapi.ContainerStatus, len(pod.Status.ContainerStatuses))
	copy(statuses, pod.Status.ContainerStatuses)
	sort.Slice(statuses, func(i, j int) bool {
		aT, bT := statuses[i].State.Terminated, statuses[j].State.Terminated
		if (aT == nil) == (bT == nil) {
			if aT == nil {
				return statuses[i].Name < statuses[j].Name
			}
			return aT.FinishedAt.Time.Before(bT.FinishedAt.Time)
		}
		if aT != nil {
			return false
		}
		return true
	})

	var lastFinished time.Time
	var tests []*junit.TestCase
	for _, status := range statuses {
		t := status.State.Terminated
		if t == nil || !names.Has(status.Name) {
			continue
		}
		if lastFinished.Before(t.StartedAt.Time) {
			lastFinished = t.StartedAt.Time
		}
		test := &junit.TestCase{
			Name:     fmt.Sprintf("%scontainer %s", prefix, status.Name),
			Duration: t.FinishedAt.Sub(lastFinished).Seconds(),
		}
		lastFinished = t.FinishedAt.Time
		if t.ExitCode != 0 {
			test.FailureOutput = &junit.FailureOutput{
				Output: t.Message,
			}
		}
		tests = append(tests, test)
	}
	sort.Slice(tests, func(i, j int) bool {
		return tests[i].Name < tests[j].Name
	})
	return tests
}

type PodClient interface {
	loggingclient.LoggingClient
	// WithNewLoggingClient returns a new instance of the PodClient that resets
	// its LoggingClient.
	WithNewLoggingClient() PodClient
	Exec(namespace, pod string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error)
	GetLogs(namespace, name string, opts *coreapi.PodLogOptions) *rest.Request
}

func NewPodClient(ctrlclient loggingclient.LoggingClient, config *rest.Config, client rest.Interface) PodClient {
	return &podClient{LoggingClient: ctrlclient, config: config, client: client}
}

type podClient struct {
	loggingclient.LoggingClient
	config *rest.Config
	client rest.Interface
}

func (c podClient) Exec(namespace, pod string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error) {
	u := c.client.Post().Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").VersionedParams(opts, scheme.ParameterCodec).URL()
	e, err := remotecommand.NewSPDYExecutor(c.config, "POST", u)
	if err != nil {
		return nil, fmt.Errorf("could not initialize a new SPDY executor: %w", err)
	}
	return e, nil
}

func (c podClient) GetLogs(namespace, name string, opts *coreapi.PodLogOptions) *rest.Request {
	return c.client.Get().Namespace(namespace).Name(name).Resource("pods").SubResource("log").VersionedParams(opts, scheme.ParameterCodec)
}

func (c podClient) WithNewLoggingClient() PodClient {
	return podClient{
		LoggingClient: c.New(),
		config:        c.config,
		client:        c.client,
	}
}

func waitForContainer(podClient PodClient, ns, name, containerName string) error {
	logrus.WithFields(logrus.Fields{
		"namespace": ns,
		"name":      name,
		"container": containerName,
	}).Trace("Waiting for container to be running.")

	ctx := context.TODO()

	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (object runtime.Object, e error) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", name).String()
			res := &corev1.PodList{}
			return res, podClient.List(ctx, res, &ctrlruntimeclient.ListOptions{Namespace: ns, Raw: &options})
		},
		WatchFunc: func(options metav1.ListOptions) (i watch.Interface, e error) {
			opts := &ctrlruntimeclient.ListOptions{
				Namespace:     ns,
				FieldSelector: fields.OneTermEqualSelector("metadata.name", name),
				Raw:           &options,
			}
			return podClient.Watch(ctx, &corev1.PodList{}, opts)
		},
	}

	// pods in this call here are always expected to exist in advance
	var podExistsPrecondition toolswatch.PreconditionFunc

	waitForContainerStatus := func(event watch.Event) (bool, error) {
		if event.Type == watch.Deleted {
			return false, fmt.Errorf("pod/%v ns/%v was deleted", name, ns)
		}
		// the outer library will handle errors, we have no pod data to review in this case
		if event.Type != watch.Added && event.Type != watch.Modified {
			return false, nil
		}
		switch pod := event.Object.(type) {
		case *corev1.Pod:
			for _, container := range pod.Status.ContainerStatuses {
				if container.Name == containerName {
					if container.State.Running != nil || container.State.Terminated != nil {
						return true, nil
					}
					break
				}
			}
		default:
			return false, fmt.Errorf("pod/%v ns/%v got an event that did not contain a pod: %v", name, ns, event.Object)
		}
		return false, nil
	}

	waitTimeout, cancel := toolswatch.ContextWithOptionalTimeout(ctx, 300*5*time.Second /*weird, but this is what it was*/)
	defer cancel()
	_, err := toolswatch.UntilWithSync(waitTimeout, lw, &corev1.Pod{}, podExistsPrecondition, waitForContainerStatus)

	// Hack to make sure this ends up in the logging client
	if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, &corev1.Pod{}); err != nil {
		logrus.WithError(err).Warn("faild to get pod after finishing watch")
	}

	return err
}

func copyArtifacts(podClient PodClient, into, ns, name, containerName string, paths []string) error {
	logrus.Tracef("Copying artifacts from %s into %s", name, into)
	var args []string
	for _, s := range paths {
		args = append(args, "-C", s, ".")
	}

	e, err := podClient.Exec(ns, name, &coreapi.PodExecOptions{
		Container: containerName,
		Stdout:    true,
		Stderr:    true,
		Command:   append([]string{"tar", "czf", "-"}, args...),
	})
	if err != nil {
		return err
	}
	r, w := io.Pipe()
	defer func() {
		if err := w.CloseWithError(fmt.Errorf("cancelled")); err != nil {
			logrus.WithError(err).Error("CloseWithError failed")
		}
	}()
	go func() {
		err := e.Stream(remotecommand.StreamOptions{
			Stdout: w,
			Stdin:  nil,
			Stderr: os.Stderr,
		})
		if err := w.CloseWithError(err); err != nil {
			logrus.WithError(err).Error("CloseWithError failed")
		}
	}()

	size := int64(0)
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("could not read gzipped artifacts: %w", err)
	}
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("could not read artifact tarball: %w", err)
		}
		name := path.Clean(h.Name)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			continue
		}
		p := filepath.Join(into, name)
		if h.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0750); err != nil {
				return fmt.Errorf("could not create target directory %s for artifacts: %w", p, err)
			}
			continue
		}
		if len(h.Linkname) > 0 {
			fmt.Fprintf(os.Stderr, "warn: ignoring link when copying artifacts to %s: %s\n", into, h.Name)
			continue
		}
		f, err := os.Create(p)
		if err != nil {
			return fmt.Errorf("could not create target file %s for artifact: %w", p, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("could not copy contents of file %s: %w", p, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("could not close copied file %s: %w", p, err)
		}
		size += h.Size
	}

	// If we're updating a substantial amount of artifacts, let the user know as a way to
	// indicate why the step took a long amount of time. Conversely, if we just got a small
	// number of files this is just noise and can be omitted to not distract from other steps.
	if size > 1*1000*1000 {
		log.Printf("Copied %0.2fMB of artifacts from %s to %s", float64(size)/1000000, name, into)
	}

	return nil
}

func removeFile(podClient PodClient, ns, name, containerName string, paths []string) error {
	e, err := podClient.Exec(ns, name, &coreapi.PodExecOptions{
		Container: containerName,
		Stdout:    true,
		Stderr:    true,
		Command:   append([]string{"rm", "-f"}, paths...),
	})
	if err != nil {
		return err
	}
	if err := e.Stream(remotecommand.StreamOptions{
		Stdout: os.Stderr,
		Stdin:  nil,
		Stderr: os.Stderr,
	}); err != nil {
		return fmt.Errorf("could not run remote command: %w", err)
	}

	return nil
}

func addPodUtils(pod *coreapi.Pod, artifactDir string, decorationConfig *prowv1.DecorationConfig, rawJobSpec string, secretsToCensor []coreapi.VolumeMount) error {
	logMount, logVolume := decorate.LogMountAndVolume()
	toolsMount, toolsVolume := decorate.ToolsMountAndVolume()
	blobStorageVolumes, blobStorageMounts, blobStorageOptions := decorate.BlobStorageOptions(*decorationConfig, false)
	blobStorageOptions.SubDir = artifactDir
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, decorate.PlaceEntrypoint(decorationConfig, toolsMount))

	wrapperOptions, err := decorate.InjectEntrypoint(&pod.Spec.Containers[0], decorationConfig.Timeout.Get(), decorationConfig.GracePeriod.Get(), "", "", false, logMount, toolsMount)
	if err != nil {
		return fmt.Errorf("could not inject entrypoint: %w", err)
	}
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, coreapi.EnvVar{Name: artifactEnv, Value: logMount.MountPath + "/artifacts"})

	sidecar, err := decorate.Sidecar(decorationConfig, blobStorageOptions, blobStorageMounts, logMount, nil, rawJobSpec, !decorate.RequirePassingEntries, decorate.IgnoreInterrupts, secretsToCensor, *wrapperOptions)
	if err != nil {
		return fmt.Errorf("could not create sidecar: %w", err)
	}
	pod.Spec.Containers = append(pod.Spec.Containers, *sidecar)

	pod.Spec.Volumes = append(pod.Spec.Volumes, logVolume, toolsVolume)
	pod.Spec.Volumes = append(pod.Spec.Volumes, blobStorageVolumes...)
	return nil
}

func artifactsContainer() coreapi.Container {
	return coreapi.Container{
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
}

type podWaitRecord map[string]struct {
	containers sets.String
	done       chan struct{}
}
type podContainersMap map[string]sets.String

// ArtifactWorker tracks pods that have completed and have an 'artifacts' container
// in them and will extract files from the container to a local directory. It also
// gathers container logs on all pods.
//
// This worker is thread safe and may be invoked in parallel.
type ArtifactWorker struct {
	dir       string
	podClient PodClient
	namespace string

	// Processing this requires the lock, so it must not be held
	// when writing into it.
	podsToDownload chan string

	lock         sync.Mutex
	remaining    podWaitRecord
	required     podContainersMap
	hasArtifacts sets.String
}

func NewArtifactWorker(podClient PodClient, artifactDir, namespace string) *ArtifactWorker {
	// stream artifacts in the background
	w := &ArtifactWorker{
		podClient: podClient,
		namespace: namespace,
		dir:       artifactDir,

		remaining:    make(podWaitRecord),
		required:     make(podContainersMap),
		hasArtifacts: sets.NewString(),

		podsToDownload: make(chan string, 4),
	}
	go w.run()
	return w
}

func (w *ArtifactWorker) run() {
	for podName := range w.podsToDownload {
		logger := logrus.WithField("pod", podName)
		logger.Trace("Processing Pod to download artifacts.")
		if err := w.downloadArtifacts(podName, w.hasArtifacts.Has(podName)); err != nil {
			logger.WithError(err).Trace("Error downloading artifacts.")
			log.Printf("error: %v", err)
		}
		// indicate we are done with this pod by removing the map entry
		w.lock.Lock()
		logger.Trace("Removing Pod from download queue.")
		if val, ok := w.remaining[podName]; ok && val.done != nil {
			close(w.remaining[podName].done)
		}
		delete(w.remaining, podName)
		w.lock.Unlock()
	}
}

func (w *ArtifactWorker) downloadArtifacts(podName string, hasArtifacts bool) error {
	logger := logrus.WithFields(logrus.Fields{"pod": podName, "hasArtifacts": hasArtifacts, "dir": w.dir})
	logger.Trace("Downloading artifacts for Pod.")
	if err := os.MkdirAll(w.dir, 0750); err != nil {
		return fmt.Errorf("unable to create artifact directory %s: %w", w.dir, err)
	}
	logger.Trace("Downloading container logs for Pod.")
	if err := gatherContainerLogsOutput(w.podClient, filepath.Join(w.dir, "container-logs"), w.namespace, podName); err != nil {
		log.Printf("error: unable to gather container logs: %v", err)
	}

	// only pods with an artifacts container should be gathered
	if !hasArtifacts {
		logger.Trace("Only logs, not artifacts requested.")
		return nil
	}

	defer func() {
		logger.Trace("Signalling to artifacts container in Pod to shut down.")
		// signal to artifacts container to gracefully shut down
		err := removeFile(w.podClient, w.namespace, podName, "artifacts", []string{"/tmp/done"})
		if err == nil || strings.Contains(err.Error(), `unable to upgrade connection: container not found ("artifacts")`) {
			return
		}
		log.Printf("error: unable to signal to artifacts container to terminate in pod %s, %v", podName, err)
	}()

	logger.Trace("Waiting for artifacts container to finish.")
	if err := waitForContainer(w.podClient, w.namespace, podName, "artifacts"); err != nil {
		return fmt.Errorf("artifacts container for pod %s unready: %w", podName, err)
	}

	logger.Trace("Copying artifacts from Pod.")
	if err := copyArtifacts(w.podClient, w.dir, w.namespace, podName, "artifacts", []string{"/tmp/artifacts"}); err != nil {
		return fmt.Errorf("unable to retrieve artifacts from pod %s: %w", podName, err)
	}
	return nil
}

func (w *ArtifactWorker) CollectFromPod(podName string, hasArtifacts []string, waitForContainers []string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	if len(hasArtifacts) > 0 {
		w.hasArtifacts.Insert(podName)
	}

	m, ok := w.remaining[podName]
	if !ok {
		m.containers = sets.NewString()
		m.done = make(chan struct{})
		w.remaining[podName] = m
	}

	r := w.required[podName]
	if r == nil {
		r = sets.NewString()
		w.required[podName] = r
	}

	for _, name := range hasArtifacts {
		if name == "artifacts" {
			continue
		}
		m.containers.Insert(name)
	}

	for _, name := range waitForContainers {
		if name == "artifacts" || !m.containers.Has(name) {
			continue
		}
		r.Insert(name)
	}
}

func (w *ArtifactWorker) Complete(podName string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	artifactContainers, ok := w.remaining[podName]
	if !ok {
		return
	}

	// when all containers in a given pod that output artifacts have completed, exit
	if artifactContainers.containers.Len() > 0 {
		w.lock.Unlock()
		w.podsToDownload <- podName
		w.lock.Lock()
	}
	if len(w.remaining) == 0 {
		close(w.podsToDownload)
	}
}

func hasFailedContainers(pod *coreapi.Pod) bool {
	for _, status := range append(append([]coreapi.ContainerStatus(nil), pod.Status.ContainerStatuses...), pod.Status.InitContainerStatuses...) {
		if status.State.Terminated != nil && status.State.Terminated.ExitCode != 0 {
			return true
		}
	}
	return false
}

func (w *ArtifactWorker) Notify(pod *coreapi.Pod, containerName string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	artifactContainers := w.remaining[pod.Name]
	requiredContainers := w.required[pod.Name]

	artifactContainers.containers.Delete(containerName)
	requiredContainers.Delete(containerName)

	// if at least one container has failed, and there are no longer any
	// remaining required containers, we don't have to wait for other artifact containers
	// to exit
	if hasFailedContainers(pod) && requiredContainers.Len() == 0 {
		for k := range artifactContainers.containers {
			artifactContainers.containers.Delete(k)
		}
	}

	// no more artifact containers, we can start grabbing artifacts
	if artifactContainers.containers.Len() == 0 {
		w.lock.Unlock()
		w.podsToDownload <- pod.Name
		w.lock.Lock()
	}

	if len(w.remaining) == 0 {
		close(w.podsToDownload)
	}
}

func (w *ArtifactWorker) Done(podName string) <-chan struct{} {
	w.lock.Lock()
	defer w.lock.Unlock()
	return w.remaining[podName].done
}

func addArtifactContainersFromPod(pod *coreapi.Pod, worker *ArtifactWorker) {
	var containers []string
	for _, container := range append(append([]coreapi.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...) {
		if !containerHasVolumeName(container, "artifacts") {
			continue
		}
		containers = append(containers, container.Name)
	}
	var waitForContainers []string
	if names := pod.Annotations[annotationWaitForContainerArtifacts]; len(names) > 0 {
		waitForContainers = strings.Split(names, ",")
	}
	worker.CollectFromPod(pod.Name, containers, waitForContainers)
}

func containerHasVolumeName(container coreapi.Container, name string) bool {
	for _, v := range container.VolumeMounts {
		if v.Name == name {
			return true
		}
	}
	return false
}

func addArtifactsToPod(pod *coreapi.Pod) {
	if hasArtifactsVolume(pod) && hasMountsArtifactsVolume(pod) {
		pod.Spec.Containers = append(pod.Spec.Containers, artifactsContainer())
	}
}

func hasArtifactsVolume(pod *coreapi.Pod) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == "artifacts" {
			return true
		}
	}
	return false
}

func hasMountsArtifactsVolume(pod *coreapi.Pod) bool {
	for _, initContainer := range pod.Spec.InitContainers {
		for _, volumeMount := range initContainer.VolumeMounts {
			if volumeMount.Name == "artifacts" {
				return true
			}
		}
	}

	for _, container := range pod.Spec.Containers {
		for _, volumeMount := range container.VolumeMounts {
			if volumeMount.Name == "artifacts" {
				return true
			}
		}
	}

	return false
}

func gatherContainerLogsOutput(podClient PodClient, artifactDir, namespace, podName string) error {
	logger := logrus.WithFields(logrus.Fields{"pod": podName, "namespace": namespace, "artifactDir": artifactDir})
	logger.Trace("Gathering container logs.")
	var validationErrors []error
	pod := &coreapi.Pod{}
	if err := podClient.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: podName}, pod); err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("could not list pod: %w", err)
	}

	if pod.Annotations[annotationSaveContainerLogs] != "true" {
		logger.Trace("Container logs not requested.")
		return nil
	}

	if err := os.MkdirAll(artifactDir, 0750); err != nil {
		return fmt.Errorf("unable to create directory %s: %w", artifactDir, err)
	}

	logger.Trace("Getting container statuses....")
	statuses := getContainerStatuses(pod)
	for _, status := range statuses {
		logger = logger.WithField("container", status.Name)
		logger.Trace("Processing container.")
		if status.State.Terminated != nil {
			logger.Trace("Container is terminated.")
			file, err := os.Create(fmt.Sprintf("%s/%s.log.gz", artifactDir, status.Name))
			if err != nil {
				validationErrors = append(validationErrors, fmt.Errorf("cannot create file: %w", err))
				continue
			}
			defer file.Close()

			w := gzip.NewWriter(file)
			logger.Trace("Fetching container logs.")
			if s, err := podClient.GetLogs(namespace, podName, &coreapi.PodLogOptions{Container: status.Name}).Stream(context.TODO()); err == nil {
				if _, err := io.Copy(w, s); err != nil {
					validationErrors = append(validationErrors, fmt.Errorf("error: Unable to copy log output from pod container %s: %w", status.Name, err))
				}
				s.Close()
			} else {
				validationErrors = append(validationErrors, fmt.Errorf("error: Unable to retrieve logs from pod container %s: %w", status.Name, err))
			}
			w.Close()
		}
	}
	return utilerrors.NewAggregate(validationErrors)
}

// for gathering successful build logs to the artifacts, there is no way to augment the pod spec
// created by the build controller to add the artifacts container; this method cherry picks elements
// from downloadArtifacts and gatherContainerLogsOutput and munges them in conjunction with the build
// api logging capabilities; also, without needing to inject an artifacts container, some of the complexities
// around download/copy from the artifacts container's volume mount and multiple pods are avoided.
func gatherSuccessfulBuildLog(buildClient BuildClient, namespace, buildName string) error {
	artifactDir, set := api.Artifacts()
	if !set {
		return nil
	}
	// adding a subdir to the artifactDir path similar to downloadArtifacts adding the container-logs subdir
	dir := filepath.Join(artifactDir, "build-logs")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("unable to create directory %s: %w", dir, err)
	}
	file, err := os.Create(fmt.Sprintf("%s/%s.log.gz", dir, buildName))
	if err != nil {
		return fmt.Errorf("cannot create file: %w", err)
	}
	defer file.Close()
	w := gzip.NewWriter(file)
	defer w.Close()
	if rc, err := buildClient.Logs(namespace, buildName, &buildapi.BuildLogOptions{}); err == nil {
		defer rc.Close()
		if _, err := io.Copy(w, rc); err != nil {
			return fmt.Errorf("error: Unable to copy log output from pod container %s: %w", buildName, err)
		}
	} else {
		return fmt.Errorf("error: Unable to retrieve logs for build %s: %w", buildName, err)
	}
	return nil
}

func getContainerStatuses(pod *coreapi.Pod) []coreapi.ContainerStatus {
	var statuses []coreapi.ContainerStatus
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)
	return statuses
}
