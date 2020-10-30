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
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/junit"
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
	// Cancel indicates that any actions the notifier is taking should be aborted immediately.
	Cancel()
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
func (nopNotifier) Cancel() {}

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
func (n *TestCaseNotifier) Cancel()                             { n.nested.Cancel() }

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

type podCmdExecutor interface {
	Exec(namespace, pod string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error)
}

type fakePodExec struct{}

func (fakePodExec) Stream(remotecommand.StreamOptions) error { return nil }

type podClient struct {
	coreclientset.PodsGetter
	config *rest.Config
	client rest.Interface
}

type fakePodsInterface struct {
	coreclientset.PodInterface
}

func (fakePodsInterface) GetLogs(string, *coreapi.PodLogOptions) *rest.Request {
	return rest.NewRequestWithClient(nil, "", rest.ClientContentConfig{}, nil)
}

type fakePodClient struct {
	coreclientset.PodsGetter
}

func (c *fakePodClient) Pods(ns string) coreclientset.PodInterface {
	return &fakePodsInterface{PodInterface: c.PodsGetter.Pods(ns)}
}

func (c *fakePodClient) Exec(namespace, name string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error) {
	return &fakePodExec{}, nil
}

func NewPodClient(podsClient coreclientset.PodsGetter, config *rest.Config, client rest.Interface) PodClient {
	return &podClient{PodsGetter: podsClient, config: config, client: client}
}

func (c podClient) Exec(namespace, pod string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error) {
	u := c.client.Post().Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").VersionedParams(opts, scheme.ParameterCodec).URL()
	e, err := remotecommand.NewSPDYExecutor(c.config, "POST", u)
	if err != nil {
		return nil, fmt.Errorf("could not initialize a new SPDY executor: %w", err)
	}
	return e, nil
}

type PodClient interface {
	coreclientset.PodsGetter
	podCmdExecutor
}

// Allow tests to accelerate time
var timeSecond = time.Second

func waitForContainer(podClient PodClient, ns, name, containerName string) error {
	logrus.WithFields(logrus.Fields{
		"namespace": ns,
		"name":      name,
		"container": containerName,
	}).Trace("Waiting for container to be running.")

	return wait.PollImmediate(time.Second, 30*timeSecond, func() (bool, error) {
		pod, err := podClient.Pods(ns).Get(context.TODO(), name, meta.GetOptions{})
		if err != nil {
			logrus.WithError(err).Errorf("Waiting for container %s in pod %s in namespace %s", containerName, name, ns)
			return false, nil
		}

		for _, container := range pod.Status.ContainerStatuses {
			if container.Name == containerName {
				if container.State.Running != nil || container.State.Terminated != nil {
					return true, nil
				}
				break
			}
		}

		return false, nil
	})
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

func addArtifacts(pod *coreapi.Pod, artifactDir string) {
	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].VolumeMounts = append(pod.Spec.Containers[i].VolumeMounts, coreapi.VolumeMount{
			Name:      "artifacts",
			MountPath: artifactDir,
		})
		pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, coreapi.EnvVar{Name: artifactEnv, Value: artifactDir})
	}
	addArtifactsContainer(pod)
}

func addArtifactsContainer(pod *coreapi.Pod) {
	pod.Spec.Containers = append(pod.Spec.Containers, artifactsContainer())
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: "artifacts",
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{},
		},
	})
}

func artifactsContainer() coreapi.Container {
	return coreapi.Container{
		Name:  "artifacts",
		Image: "busybox",
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
		if err := w.downloadArtifacts(podName, w.hasArtifacts.Has(podName)); err != nil {
			log.Printf("error: %v", err)
		}
		// indicate we are done with this pod by removing the map entry
		w.lock.Lock()
		close(w.remaining[podName].done)
		delete(w.remaining, podName)
		w.lock.Unlock()
	}
}

func (w *ArtifactWorker) downloadArtifacts(podName string, hasArtifacts bool) error {
	if err := os.MkdirAll(w.dir, 0750); err != nil {
		return fmt.Errorf("unable to create artifact directory %s: %w", w.dir, err)
	}
	if err := gatherContainerLogsOutput(w.podClient, filepath.Join(w.dir, "container-logs"), w.namespace, podName); err != nil {
		log.Printf("error: unable to gather container logs: %v", err)
	}

	// only pods with an artifacts container should be gathered
	if !hasArtifacts {
		return nil
	}

	defer func() {
		// signal to artifacts container to gracefully shut down
		err := removeFile(w.podClient, w.namespace, podName, "artifacts", []string{"/tmp/done"})
		if err == nil || strings.Contains(err.Error(), `unable to upgrade connection: container not found ("artifacts")`) {
			return
		}
		log.Printf("error: unable to signal to artifacts container to terminate in pod %s, %v", podName, err)
	}()

	if err := waitForContainer(w.podClient, w.namespace, podName, "artifacts"); err != nil {
		return fmt.Errorf("artifacts container for pod %s unready: %w", podName, err)
	}

	if err := copyArtifacts(w.podClient, w.dir, w.namespace, podName, "artifacts", []string{"/tmp/artifacts"}); err != nil {
		return fmt.Errorf("unable to retrieve artifacts from pod %s: %w", podName, err)
	}
	return nil
}

func (w *ArtifactWorker) CollectFromPod(podName string, hasArtifacts []string, waitForContainers []string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	w.hasArtifacts.Insert(podName)

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
	if artifactContainers.containers.Len() > 0 {
		// when all containers in a given pod that output artifacts have completed, exit
		w.podsToDownload <- podName
	}
	if len(w.remaining) == 0 {
		close(w.podsToDownload)
	}
}

func (w *ArtifactWorker) Cancel() {
	w.lock.Lock()
	defer w.lock.Unlock()
	for podName := range w.remaining {
		if !w.hasArtifacts.Has(podName) {
			continue
		}
		go func(podName string) {
			if err := removeFile(w.podClient, w.namespace, podName, "artifacts", []string{"/tmp/done"}); err != nil {
				logrus.WithError(err).Error("failed to remove file")
			}
		}(podName)
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
	if !artifactContainers.containers.Has(containerName) {
		return
	}
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
		w.podsToDownload <- pod.Name
	}
	// no more pods, we can shutdown the worker gracefully
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
	var validationErrors []error
	list, err := podClient.Pods(namespace).List(context.TODO(), meta.ListOptions{FieldSelector: fields.Set{"metadata.name": podName}.AsSelector().String()})
	if err != nil {
		return fmt.Errorf("could not list pod: %w", err)
	}
	if len(list.Items) == 0 {
		return nil
	}
	pod := &list.Items[0]

	if pod.Annotations[annotationSaveContainerLogs] != "true" {
		return nil
	}

	if err := os.MkdirAll(artifactDir, 0750); err != nil {
		return fmt.Errorf("unable to create directory %s: %w", artifactDir, err)
	}

	statuses := getContainerStatuses(pod)
	for _, status := range statuses {
		if status.State.Terminated != nil {
			file, err := os.Create(fmt.Sprintf("%s/%s.log.gz", artifactDir, status.Name))
			if err != nil {
				validationErrors = append(validationErrors, fmt.Errorf("cannot create file: %w", err))
				continue
			}
			defer file.Close()

			w := gzip.NewWriter(file)
			if s, err := podClient.Pods(namespace).GetLogs(podName, &coreapi.PodLogOptions{Container: status.Name}).Stream(context.TODO()); err == nil {
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
	return kerrors.NewAggregate(validationErrors)
}

// for gathering successful build logs to the artifacts, there is no way to augment the pod spec
// created by the build controller to add the artifacts container; this method cherry picks elements
// from downloadArtifacts and gatherContainerLogsOutput and munges them in conjunction with the build
// api logging capabilities; also, without needing to inject an artifacts container, some of the complexities
// around download/copy from the artifacts container's volume mount and multiple pods are avoided.
func gatherSuccessfulBuildLog(buildClient BuildClient, artifactDir, namespace, buildName string) error {
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
