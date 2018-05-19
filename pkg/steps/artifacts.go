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
	"sort"
	"strings"
	"sync"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	templateapi "github.com/openshift/api/template/v1"
)

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
		return err
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
		return err
	}
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		name := path.Clean(h.Name)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			continue
		}
		p := filepath.Join(into, name)
		if h.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0750); err != nil {
				return err
			}
			continue
		}
		if len(h.Linkname) > 0 {
			fmt.Fprintf(os.Stderr, "warn: ignoring link when copying artifacts to %s: %s\n", into, h.Name)
			continue
		}
		f, err := os.Create(p)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
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
		return err
	}
	if err := e.Stream(remotecommand.StreamOptions{
		Stdout: os.Stderr,
		Stdin:  nil,
		Stderr: os.Stderr,
	}); err != nil {
		return err
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

type namedContainer struct {
	pod       *coreapi.Pod
	container string
}

type artifactWorker struct {
	podClient PodClient
	namespace string

	ch chan namedContainer

	lock      sync.Mutex
	remaining podContainersMap
}

func newPodArtifactWorker(podClient PodClient, dir, namespace, name string, podsWithArtifacts podContainersMap) (ContainerNotifier, error) {
	if len(podsWithArtifacts) == 0 {
		return nil, nil
	}
	if len(dir) == 0 {
		return nil, nil
	}
	artifactDir := filepath.Join(dir, name)
	if err := os.MkdirAll(artifactDir, 0750); err != nil {
		return nil, err
	}

	var pods []string
	for podName := range podsWithArtifacts {
		pods = append(pods, podName)
	}
	sort.Strings(pods)
	log.Printf("Artifacts will be gathered from the following pods: %s", strings.Join(pods, ", "))

	// stream artifacts in the background
	w := &artifactWorker{
		podClient: podClient,
		namespace: namespace,

		remaining: podsWithArtifacts,

		ch: make(chan namedContainer, 4),
	}
	go w.run(artifactDir)

	// track which containers still need artifacts collected before terminating the artifact container
	return w, nil
}

func (w *artifactWorker) run(artifactDir string) {
	for c := range w.ch {
		log.Printf("DEBUG: starting copy for %s", c.pod.Name)
		if err := copyArtifacts(w.podClient, artifactDir, c.pod.Namespace, c.pod.Name, "artifacts", []string{"/tmp/artifacts"}); err != nil {
			log.Printf("error: Unable to retrieve artifacts from pod %s: %v", c.pod.Name, err)
		}
		if err := removeFile(w.podClient, c.pod.Namespace, c.pod.Name, "artifacts", []string{"/tmp/done"}); err != nil {
			log.Printf("error: Unable to signal to artifacts container to terminate in pod %s: %v", c.pod.Name, err)
		}
		log.Printf("DEBUG: done copying for %s", c.pod.Name)
		// indicate we are done with this pod by removing the map entry
		w.lock.Lock()
		delete(w.remaining, c.pod.Name)
		w.lock.Unlock()
	}
}

func (w *artifactWorker) Cancel() {
	w.lock.Lock()
	defer w.lock.Unlock()
	for podName := range w.remaining {
		go func(podName string) {
			removeFile(w.podClient, w.namespace, podName, "artifacts", []string{"/tmp/done"})
		}(podName)
	}
}

func (w *artifactWorker) Notify(pod *coreapi.Pod, containerName string) {
	w.lock.Lock()
	defer w.lock.Unlock()
	artifactContainers := w.remaining[pod.Name]
	if _, ok := artifactContainers[containerName]; !ok {
		return
	}
	delete(artifactContainers, containerName)
	if len(artifactContainers) == 0 {
		log.Printf("DEBUG: no more containers in %s", pod.Name)
		// when all containers in a given pod that output artifacts have completed, exit
		w.ch <- namedContainer{pod: pod, container: containerName}
	}
	if len(w.remaining) == 0 {
		log.Printf("DEBUG: no more pods")
		close(w.ch)
	}
}

func (w *artifactWorker) Done(podName string) bool {
	w.lock.Lock()
	defer w.lock.Unlock()
	log.Printf("DEBUG: remaining containers for pod %s %v", podName, w.remaining[podName])
	_, ok := w.remaining[podName]
	return !ok
}

func addArtifactContainersFromPod(pod *coreapi.Pod, artifacts map[string]map[string]struct{}) map[string]map[string]struct{} {
	s := artifacts[pod.Name]
	if s == nil {
		s = make(map[string]struct{})
		artifacts[pod.Name] = s
	}
	for _, container := range append(append([]coreapi.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...) {
		if container.Name == "artifacts" {
			continue
		}
		if containerHasVolumeName(container, "artifacts") {
			s[container.Name] = struct{}{}
		}
	}
	return artifacts
}

func containerHasVolumeName(container coreapi.Container, name string) bool {
	for _, v := range container.VolumeMounts {
		if v.Name == name {
			return true
		}
	}
	return true
}

func addArtifactsToTemplate(template *templateapi.Template) map[string]map[string]struct{} {
	allPodsWithArtifacts := make(map[string]map[string]struct{})
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
		allPodsWithArtifacts[jsonString(pod, "metadata", "name")] = names
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
	return allPodsWithArtifacts
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
