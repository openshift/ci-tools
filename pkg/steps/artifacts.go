package steps

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-operator/pkg/junit"
)

const (
	// A comma-delimited list of containers to wait for artifacts from within a pod. If not
	// specify, only 'artifacts' is waited for.
	annotationWaitForContainerArtifacts = "ci-operator.openshift.io/wait-for-container-artifacts"
	// A comma-delimited list of container names that will be returned as individual JUnit
	// test results.
	annotationContainersForSubTestResults = "ci-operator.openshift.io/container-sub-tests"
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
	// Done returns true if the specified pod name has already completed any work it had pending.
	Done(podName string) bool
	// Cancel indicates that any actions the notifier is taking should be aborted immediately.
	Cancel()
}

// NopNotifier takes no action when notified.
var NopNotifier = nopNotifier{}

type nopNotifier struct{}

func (nopNotifier) Notify(_ *coreapi.Pod, _ string) {}
func (nopNotifier) Complete(_ string)               {}
func (nopNotifier) Done(_ string) bool              { return true }
func (nopNotifier) Cancel()                         {}

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

func (n *TestCaseNotifier) Complete(podName string)  { n.nested.Complete(podName) }
func (n *TestCaseNotifier) Done(podName string) bool { return n.nested.Done(podName) }
func (n *TestCaseNotifier) Cancel()                  { n.nested.Cancel() }

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
	var tests []*junit.TestCase
	for _, status := range pod.Status.ContainerStatuses {
		if names.Has(status.Name) {
			if t := status.State.Terminated; t != nil {
				test := &junit.TestCase{
					Name:     fmt.Sprintf("%scontainer %s", prefix, status.Name),
					Duration: t.FinishedAt.Sub(t.StartedAt.Time).Seconds(),
				}
				if t.ExitCode != 0 {
					test.FailureOutput = &junit.FailureOutput{
						Output: t.Message,
					}
				}
				tests = append(tests, test)
				continue
			}
		}
	}
	return tests
}

func stringInSlice(arr []string, s string) bool {
	for _, item := range arr {
		if item == s {
			return true
		}
	}
	return false
}

type podClient struct {
	coreclientset.PodsGetter
	config *rest.Config
	client rest.Interface
}

func NewPodClient(podsClient coreclientset.PodsGetter, config *rest.Config, client rest.Interface) PodClient {
	return &podClient{PodsGetter: podsClient, config: config, client: client}
}

func (c *podClient) RESTConfig() *rest.Config   { return c.config }
func (c *podClient) RESTClient() rest.Interface { return c.client }

type PodClient interface {
	coreclientset.PodsGetter
	RESTConfig() *rest.Config
	RESTClient() rest.Interface
}

func copyArtifacts(podClient PodClient, into, ns, name, containerName string, paths []string) error {
	log.Printf("Copying artifacts from %s into %s", name, into)
	var args []string
	for _, s := range paths {
		args = append(args, "-C", s, ".")
	}

	u := podClient.RESTClient().Post().Resource("pods").Namespace(ns).Name(name).SubResource("exec").VersionedParams(&coreapi.PodExecOptions{
		Container: containerName,
		Stdout:    true,
		Stderr:    true,
		Command:   append([]string{"tar", "czf", "-"}, args...),
	}, scheme.ParameterCodec).URL()

	e, err := remotecommand.NewSPDYExecutor(podClient.RESTConfig(), "POST", u)
	if err != nil {
		return fmt.Errorf("could not initialize a new SPDY executor: %v", err)
	}
	r, w := io.Pipe()
	defer w.CloseWithError(fmt.Errorf("cancelled"))
	go func() {
		err := e.Stream(remotecommand.StreamOptions{
			Stdout: w,
			Stdin:  nil,
			Stderr: os.Stderr,
		})
		w.CloseWithError(err)
	}()

	size := int64(0)
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("could not read gzipped artifacts: %v", err)
	}
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("could not read artifact tarball: %v", err)
		}
		name := path.Clean(h.Name)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			continue
		}
		p := filepath.Join(into, name)
		if h.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0750); err != nil {
				return fmt.Errorf("could not create target directory %s for artifacts: %v", p, err)
			}
			continue
		}
		if len(h.Linkname) > 0 {
			fmt.Fprintf(os.Stderr, "warn: ignoring link when copying artifacts to %s: %s\n", into, h.Name)
			continue
		}
		f, err := os.Create(p)
		if err != nil {
			return fmt.Errorf("could not create target file %s for artifact: %v", p, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("could not copy contents of file %s: %v", p, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("could not close copied file %s: %v", p, err)
		}
		size += h.Size
	}

	if size > 0 {
		log.Printf("Copied %0.2fMi of artifacts from %s to %s", float64(size)/1000000, name, into)
	}

	return nil
}

func removeFile(podClient PodClient, ns, name, containerName string, paths []string) error {
	u := podClient.RESTClient().Post().Resource("pods").Namespace(ns).Name(name).SubResource("exec").VersionedParams(&coreapi.PodExecOptions{
		Container: containerName,
		Stdout:    true,
		Stderr:    true,
		Command:   append([]string{"rm", "-f"}, paths...),
	}, scheme.ParameterCodec).URL()

	e, err := remotecommand.NewSPDYExecutor(podClient.RESTConfig(), "POST", u)
	if err != nil {
		return fmt.Errorf("could not initialize a new SPDY executor: %v", err)
	}
	if err := e.Stream(remotecommand.StreamOptions{
		Stdout: os.Stderr,
		Stdin:  nil,
		Stderr: os.Stderr,
	}); err != nil {
		return fmt.Errorf("could not run remote command: %v", err)
	}

	return nil
}

func addArtifactsContainer(pod *coreapi.Pod, artifactDir string) {
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
		echo "Artifacts extracted"
		exit 0
	fi
	sleep 5 & wait
done
`,
		},
	}
}

type podContainersMap map[string]map[string]struct{}

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
	remaining    podContainersMap
	required     podContainersMap
	hasArtifacts sets.String
}

func NewArtifactWorker(podClient PodClient, artifactDir, namespace string) *ArtifactWorker {
	// stream artifacts in the background
	w := &ArtifactWorker{
		podClient: podClient,
		namespace: namespace,
		dir:       artifactDir,

		remaining:    make(podContainersMap),
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
		delete(w.remaining, podName)
		w.lock.Unlock()
	}
}

func (w *ArtifactWorker) downloadArtifacts(podName string, hasArtifacts bool) error {
	if err := os.MkdirAll(w.dir, 0750); err != nil {
		return fmt.Errorf("unable to create artifact directory %s: %v", w.dir, err)
	}
	if err := gatherContainerLogsOutput(w.podClient, filepath.Join(w.dir, "container-logs"), w.namespace, podName); err != nil {
		log.Printf("error: unable to gather container logs: %v", err)
	}

	// only pods with an artifacts container should be gathered
	if !hasArtifacts {
		return nil
	}

	defer func() {
		// signal to artifacts container to gracefully shut don
		err := removeFile(w.podClient, w.namespace, podName, "artifacts", []string{"/tmp/done"})
		if err == nil {
			return
		}
		log.Printf("error: unable to signal to artifacts container to terminate in pod %s, triggering deletion: %v", podName, err)

		// attempt to delete the pod
		err = w.podClient.Pods(w.namespace).Delete(podName, nil)
		if err == nil || errors.IsNotFound(err) {
			return
		}
		log.Printf("error: unable to retrieve artifacts from pod %s and the pod could not be deleted: %v", podName, err)

		// give up, expect another process to clean up the pods
	}()

	if err := copyArtifacts(w.podClient, w.dir, w.namespace, podName, "artifacts", []string{"/tmp/artifacts"}); err != nil {
		return fmt.Errorf("unable to retrieve artifacts from pod %s: %v", podName, err)
	}
	return nil
}

func (w *ArtifactWorker) CollectFromPod(podName string, hasArtifactsContainer bool, hasArtifacts []string, waitForContainers []string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	w.hasArtifacts.Insert(podName)

	m := w.remaining[podName]
	if m == nil {
		m = make(map[string]struct{})
		w.remaining[podName] = m
	}

	r := w.required[podName]
	if r == nil {
		r = make(map[string]struct{})
		w.required[podName] = r
	}

	for _, name := range hasArtifacts {
		if name == "artifacts" {
			continue
		}
		if _, ok := m[name]; !ok {
			m[name] = struct{}{}
		}
	}

	for _, name := range waitForContainers {
		if name == "artifacts" {
			continue
		}
		if _, ok := m[name]; !ok {
			continue
		}
		if _, ok := r[name]; !ok {
			r[name] = struct{}{}
		}
	}
}

func (w *ArtifactWorker) Complete(podName string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	artifactContainers, ok := w.remaining[podName]
	if !ok {
		return
	}
	if len(artifactContainers) > 0 {
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
			removeFile(w.podClient, w.namespace, podName, "artifacts", []string{"/tmp/done"})
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
	if _, ok := artifactContainers[containerName]; !ok {
		return
	}
	requiredContainers := w.required[pod.Name]

	delete(artifactContainers, containerName)
	delete(requiredContainers, containerName)

	// if at least one container has failed, and there are no longer any
	// remaining required containers, we don't have to wait for other artifact containers
	// to exit
	if hasFailedContainers(pod) && len(requiredContainers) == 0 {
		for k := range artifactContainers {
			delete(artifactContainers, k)
		}
	}
	// no more artifact containers, we can start grabbing artifacts
	if len(artifactContainers) == 0 {
		w.podsToDownload <- pod.Name
	}
	// no more pods, we can shutdown the worker gracefully
	if len(w.remaining) == 0 {
		close(w.podsToDownload)
	}
}

func (w *ArtifactWorker) Done(podName string) bool {
	w.lock.Lock()
	defer w.lock.Unlock()
	// log.Printf("DEBUG: remaining containers for pod %s %v", podName, w.remaining[podName])
	_, ok := w.remaining[podName]
	return !ok
}

func addArtifactContainersFromPod(pod *coreapi.Pod, worker *ArtifactWorker) {
	var containers []string
	var hasArtifactsContainer bool
	for _, container := range append(append([]coreapi.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...) {
		if container.Name == "artifacts" {
			hasArtifactsContainer = true
		}
		if !containerHasVolumeName(container, "artifacts") {
			continue
		}
		containers = append(containers, container.Name)
	}
	var waitForContainers []string
	if names := pod.Annotations[annotationWaitForContainerArtifacts]; len(names) > 0 {
		waitForContainers = strings.Split(names, ",")
	}
	worker.CollectFromPod(pod.Name, hasArtifactsContainer, containers, waitForContainers)
}

func containerHasVolumeName(container coreapi.Container, name string) bool {
	for _, v := range container.VolumeMounts {
		if v.Name == name {
			return true
		}
	}
	return true
}

func addArtifactsToTemplate(template *templateapi.Template) {
	for i := range template.Objects {
		t := &template.Objects[i]
		var pod map[string]interface{}
		if err := json.Unmarshal(t.Raw, &pod); err != nil {
			log.Printf("error: object can't be unmarshalled: %v", err)
			continue
		}
		if jsonString(pod, "kind") != "Pod" || jsonString(pod, "apiVersion") != "v1" {
			continue
		}
		if !arrayHasObjectString(jsonArray(pod, "spec", "volumes"), "name", "artifacts") {
			continue
		}
		names := allPodContainerNamesWithArtifacts(pod)
		if len(names) == 0 {
			continue
		}
		data, err := json.Marshal(artifactsContainer())
		if err != nil {
			panic(err)
		}
		var container map[string]interface{}
		if err := json.Unmarshal(data, &container); err != nil {
			panic(err)
		}
		containers := append(jsonArray(pod, "spec", "containers"), container)
		jsonMap(pod, "spec")["containers"] = containers
		data, err = json.Marshal(pod)
		if err != nil {
			panic(err)
		}
		t.Object = nil
		t.Raw = data
	}
}

func jsonMap(obj map[string]interface{}, keys ...string) map[string]interface{} {
	if len(keys) == 0 {
		return obj
	}
	for _, key := range keys[:len(keys)-1] {
		v, ok := obj[key]
		if !ok {
			return nil
		}
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil
		}
		obj = m
	}
	m, _ := obj[keys[len(keys)-1]].(map[string]interface{})
	return m
}

func jsonArray(obj map[string]interface{}, keys ...string) []interface{} {
	if len(keys) < 1 {
		return nil
	}
	s, _ := jsonMap(obj, keys[:len(keys)-1]...)[keys[len(keys)-1]].([]interface{})
	return s
}

func jsonString(obj map[string]interface{}, keys ...string) string {
	if len(keys) < 1 {
		return ""
	}
	s, _ := jsonMap(obj, keys[:len(keys)-1]...)[keys[len(keys)-1]].(string)
	return s
}

func arrayHasObjectString(arr []interface{}, key, name string) bool {
	for _, obj := range arr {
		o, _ := obj.(map[string]interface{})
		if jsonString(o, key) == name {
			return true
		}
	}
	return false
}

func allPodContainerNamesWithArtifacts(pod map[string]interface{}) map[string]struct{} {
	names := make(map[string]struct{})
	for _, obj := range append(append([]interface{}(nil), jsonArray(pod, "spec", "initContainers")...), jsonArray(pod, "spec", "containers")...) {
		o, _ := obj.(map[string]interface{})
		if arrayHasObjectString(jsonArray(o, "volumeMounts"), "name", "artifacts") {
			names[jsonString(o, "name")] = struct{}{}
		}
	}
	return names
}

func gatherContainerLogsOutput(podClient PodClient, artifactDir, namespace, podName string) error {
	var validationErrors []error
	list, err := podClient.Pods(namespace).List(meta.ListOptions{FieldSelector: fields.Set{"metadata.name": podName}.AsSelector().String()})
	if err != nil {
		return fmt.Errorf("could not list pod: %v", err)
	}
	pod := &list.Items[0]

	if pod.Annotations["ci-operator.openshift.io/save-container-logs"] != "true" {
		return nil
	}

	if err := os.MkdirAll(artifactDir, 0750); err != nil {
		return fmt.Errorf("unable to create directory %s: %v", artifactDir, err)
	}

	statuses := getContainerStatuses(pod)
	for _, status := range statuses {
		if status.State.Terminated != nil {
			file, err := os.Create(fmt.Sprintf("%s/%s.log.gz", artifactDir, status.Name))
			if err != nil {
				validationErrors = append(validationErrors, fmt.Errorf("Cannot create file: %v", err))
				continue
			}
			defer file.Close()

			w := gzip.NewWriter(file)
			if s, err := podClient.Pods(namespace).GetLogs(podName, &coreapi.PodLogOptions{Container: status.Name}).Stream(); err == nil {
				if _, err := io.Copy(w, s); err != nil {
					validationErrors = append(validationErrors, fmt.Errorf("error: Unable to copy log output from pod container %s: %v", status.Name, err))
				}
				s.Close()
			} else {
				validationErrors = append(validationErrors, fmt.Errorf("error: Unable to retrieve logs from pod container %s: %v", status.Name, err))
			}
			w.Close()
		}
	}
	return kerrors.NewAggregate(validationErrors)
}

func getContainerStatuses(pod *coreapi.Pod) []coreapi.ContainerStatus {
	var statuses []coreapi.ContainerStatus
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)
	return statuses
}
