package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/rest"

	templateapi "github.com/openshift/api/template/v1"
	templateclientset "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/ci-operator/pkg/api"
)

type templateExecutionStep struct {
	template       *templateapi.Template
	params         *DeferredParameters
	templateClient TemplateClient
	podClient      coreclientset.PodsGetter
	jobSpec        *JobSpec
}

func (s *templateExecutionStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *templateExecutionStep) Run(ctx context.Context, dry bool) error {
	log.Printf("Executing template %s", s.template.Name)

	if len(s.template.Objects) == 0 {
		return fmt.Errorf("template %s has no objects", s.template.Name)
	}

	for i, p := range s.template.Parameters {
		if len(p.Value) == 0 {
			if !s.params.Has(p.Name) && !strings.HasPrefix(p.Name, "IMAGE_") && p.Required {
				return fmt.Errorf("template %s has required parameter %s which is not defined", s.template.Name, p.Name)
			}
		}
		if s.params.Has(p.Name) {
			value, err := s.params.Get(p.Name)
			if err != nil {
				if !dry {
					return fmt.Errorf("cannot resolve parameter %s into template %s: %v", p.Name, s.template.Name, err)
				}
			}
			if len(value) > 0 {
				s.template.Parameters[i].Value = value
			}
			continue
		}
		if strings.HasPrefix(p.Name, "IMAGE_") {
			component := strings.ToLower(strings.TrimPrefix(p.Name, "IMAGE_"))
			if len(component) > 0 {
				component = strings.Replace(component, "_", "-", -1)
				format, err := s.params.Get("IMAGE_FORMAT")
				if err != nil {
					return err
				}
				s.template.Parameters[i].Value = strings.Replace(format, componentFormatReplacement, component, -1)
			}
		}
	}

	if dry {
		j, _ := json.MarshalIndent(s.template, "", "  ")
		log.Printf("template:\n%s", j)
		return nil
	}

	go func() {
		<-ctx.Done()
		log.Printf("cleanup: Deleting template %s", s.template.Name)
		if err := s.templateClient.TemplateInstances(s.jobSpec.Namespace()).Delete(s.template.Name, nil); err != nil && !errors.IsNotFound(err) {
			log.Printf("error: Could not delete template instance: %v", err)
		}
	}()

	// TODO: enforce single namespace behavior
	instance := &templateapi.TemplateInstance{
		ObjectMeta: meta.ObjectMeta{
			Name: s.template.Name,
		},
		Spec: templateapi.TemplateInstanceSpec{
			Template: *s.template,
		},
	}
	if owner := s.jobSpec.Owner(); owner != nil {
		instance.OwnerReferences = append(instance.OwnerReferences, *owner)
	}

	instance, err := createOrRestartTemplateInstance(s.templateClient.TemplateInstances(s.jobSpec.Namespace()), s.podClient.Pods(s.jobSpec.Namespace()), instance)
	if err != nil {
		return err
	}

	instance, err = waitForTemplateInstanceReady(s.templateClient.TemplateInstances(s.jobSpec.Namespace()), instance)
	if err != nil {
		return err
	}

	for _, ref := range instance.Status.Objects {
		switch {
		case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
			log.Printf("Running pod %s", ref.Ref.Name)
		}
	}
	for _, ref := range instance.Status.Objects {
		switch {
		case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
			if err := waitForPodCompletion(s.podClient.Pods(s.jobSpec.Namespace()), ref.Ref.Name); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *templateExecutionStep) Done() (bool, error) {
	instance, err := s.templateClient.TemplateInstances(s.jobSpec.Namespace()).Get(s.template.Name, meta.GetOptions{})
	if errors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("unable to retrieve existing template: %v", err)
	}
	ready, err := templateInstanceReady(instance)
	if err != nil {
		return false, err
	}
	if !ready {
		return false, nil
	}
	for _, ref := range instance.Status.Objects {
		switch {
		case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
			ready, err := isPodCompleted(s.podClient.Pods(s.jobSpec.Namespace()), ref.Ref.Name)
			if err != nil {
				return false, err
			}
			if !ready {
				return false, nil
			}
		}
	}
	return true, nil
}

func (s *templateExecutionStep) Requires() []api.StepLink {
	var links []api.StepLink
	for _, p := range s.template.Parameters {
		if s.params.Has(p.Name) {
			links = append(links, s.params.Links(p.Name)...)
			continue
		}
		if strings.HasPrefix(p.Name, "IMAGE_") {
			links = append(links, s.params.Links("IMAGE_FORMAT")...)
			continue
		}
	}
	return links
}

func (s *templateExecutionStep) Creates() []api.StepLink {
	return []api.StepLink{}
}

func (s *templateExecutionStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}

func (s *templateExecutionStep) Name() string { return s.template.Name }

func TemplateExecutionStep(template *templateapi.Template, params *DeferredParameters, podClient coreclientset.PodsGetter, templateClient TemplateClient, jobSpec *JobSpec) api.Step {
	return &templateExecutionStep{
		template:       template,
		params:         params,
		podClient:      podClient,
		templateClient: templateClient,
		jobSpec:        jobSpec,
	}
}

type DeferredParameters struct {
	lock   sync.Mutex
	fns    api.ParameterMap
	values map[string]string
	links  map[string][]api.StepLink
}

func NewDeferredParameters() *DeferredParameters {
	return &DeferredParameters{
		fns:    make(api.ParameterMap),
		values: make(map[string]string),
		links:  make(map[string][]api.StepLink),
	}
}

func (p *DeferredParameters) Map() (map[string]string, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	m := make(map[string]string)
	for k, fn := range p.fns {
		if v, ok := p.values[k]; ok {
			m[k] = v
			continue
		}
		v, err := fn()
		if err != nil {
			return nil, err
		}
		p.values[k] = v
		m[k] = v
	}
	return m, nil
}

func (p *DeferredParameters) Add(name string, link api.StepLink, fn func() (string, error)) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.fns[name] = fn
	if link != nil {
		p.links[name] = []api.StepLink{link}
	}
}

func (p *DeferredParameters) Has(name string) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	_, ok := p.fns[name]
	if ok {
		return true
	}
	_, ok = os.LookupEnv(name)
	return ok
}

func (p *DeferredParameters) Links(name string) []api.StepLink {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, ok := os.LookupEnv(name); ok {
		return nil
	}
	return p.links[name]
}

func (p *DeferredParameters) AllLinks() []api.StepLink {
	p.lock.Lock()
	defer p.lock.Unlock()
	var links []api.StepLink
	for name, v := range p.links {
		if _, ok := os.LookupEnv(name); ok {
			continue
		}
		links = append(links, v...)
	}
	return links
}

func (p *DeferredParameters) Get(name string) (string, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if value, ok := p.values[name]; ok {
		return value, nil
	}
	if value, ok := os.LookupEnv(name); ok {
		p.values[name] = value
		return value, nil
	}
	if fn, ok := p.fns[name]; ok {
		value, err := fn()
		if err != nil {
			return "", err
		}
		p.values[name] = value
		return value, nil
	}
	return "", nil
}

type TemplateClient interface {
	templateclientset.TemplateV1Interface
	Process(namespace string, template *templateapi.Template) (*templateapi.Template, error)
}

type templateClient struct {
	templateclientset.TemplateV1Interface
	restClient rest.Interface
}

func NewTemplateClient(client templateclientset.TemplateV1Interface, restClient rest.Interface) TemplateClient {
	return &templateClient{
		TemplateV1Interface: client,
		restClient:          restClient,
	}
}

func (c *templateClient) Process(namespace string, template *templateapi.Template) (*templateapi.Template, error) {
	processed := &templateapi.Template{}
	err := c.restClient.Post().
		Namespace(namespace).
		Resource("processedtemplates").
		Body(template).
		Do().
		Into(processed)
	return processed, err
}

func isPodCompleted(podClient coreclientset.PodInterface, name string) (bool, error) {
	pod, err := podClient.Get(name, meta.GetOptions{})
	if errors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pod.Status.Phase == coreapi.PodSucceeded || pod.Status.Phase == coreapi.PodFailed {
		return true, nil
	}
	for _, status := range pod.Status.InitContainerStatuses {
		if s := status.State.Terminated; s != nil {
			if s.ExitCode != 0 {
				return true, nil
			}
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if s := status.State.Terminated; s != nil {
			if s.ExitCode != 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

func waitForTemplateInstanceReady(templateClient templateclientset.TemplateInstanceInterface, instance *templateapi.TemplateInstance) (*templateapi.TemplateInstance, error) {
	for {
		ready, err := templateInstanceReady(instance)
		if err != nil {
			return nil, err
		}
		if ready {
			return instance, nil
		}

		time.Sleep(2 * time.Second)
		instance, err = templateClient.Get(instance.Name, meta.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve existing template instance: %v", err)
		}
	}
}

func createOrRestartTemplateInstance(templateClient templateclientset.TemplateInstanceInterface, podClient coreclientset.PodInterface, instance *templateapi.TemplateInstance) (*templateapi.TemplateInstance, error) {
	if err := waitForCompletedTemplateInstanceDeletion(templateClient, podClient, instance.Name); err != nil {
		return nil, fmt.Errorf("unable to delete completed template instance: %v", err)
	}
	created, err := templateClient.Create(instance)
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("unable to create template instance: %v", err)
	}
	if err != nil {
		created, err = templateClient.Get(instance.Name, meta.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve pod: %v", err)
		}
		log.Printf("Waiting for running template %s to finish", instance.Name)
	}
	return created, nil
}

func waitForCompletedTemplateInstanceDeletion(templateClient templateclientset.TemplateInstanceInterface, podClient coreclientset.PodInterface, name string) error {
	instance, err := templateClient.Get(name, meta.GetOptions{})
	if errors.IsNotFound(err) {
		return nil
	}

	// if the instance is running
	if instance.DeletionTimestamp == nil {
		ok, err := templateInstanceReady(instance)
		if err != nil {
			return err
		}
		// creating template instances are left to run
		if !ok {
			return nil
		}

		// if any of the pods referenced by the template are still running, just continue
		for _, ref := range instance.Status.Objects {
			switch {
			case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
				ok, err := isPodCompleted(podClient, ref.Ref.Name)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
			}
		}
	}

	// delete the instance we had before, otherwise another user has relaunched this template
	uid := instance.UID
	policy := meta.DeletePropagationForeground
	err = templateClient.Delete(name, &meta.DeleteOptions{
		PropagationPolicy: &policy,
		Preconditions:     &meta.Preconditions{UID: &uid},
	})
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	log.Printf("Waiting for template instance %s to be deleted ...", name)
	for {
		instance, err := templateClient.Get(name, meta.GetOptions{})
		if errors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if instance.UID != uid {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}

func createOrRestartPod(podClient coreclientset.PodInterface, pod *coreapi.Pod) (*coreapi.Pod, error) {
	if err := waitForCompletedPodDeletion(podClient, pod.Name); err != nil {
		return nil, fmt.Errorf("unable to delete completed pod: %v", err)
	}
	created, err := podClient.Create(pod)
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("unable to create pod: %v", err)
	}
	if err != nil {
		created, err = podClient.Get(pod.Name, meta.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve pod: %v", err)
		}
		log.Printf("Waiting for running pod %s to finish", pod.Name)
	}
	return created, nil
}

func waitForCompletedPodDeletion(podClient coreclientset.PodInterface, name string) error {
	pod, err := podClient.Get(name, meta.GetOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	// running pods are left to run, we just wait for them to finish
	if pod.Status.Phase != coreapi.PodSucceeded && pod.Status.Phase != coreapi.PodFailed && pod.DeletionTimestamp == nil {
		return nil
	}

	// delete the pod we expect, otherwise another user has relaunched this pod
	uid := pod.UID
	err = podClient.Delete(name, &meta.DeleteOptions{Preconditions: &meta.Preconditions{UID: &uid}})
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	for {
		pod, err := podClient.Get(name, meta.GetOptions{})
		if errors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if pod.UID != uid {
			return nil
		}
		log.Printf("Waiting for pod %s to be deleted ...", name)
		time.Sleep(2 * time.Second)
	}
}

func waitForPodCompletion(podClient coreclientset.PodInterface, name string) error {
	completed := make(map[string]time.Time)
	for {
		retry, err := waitForPodCompletionOrTimeout(podClient, name, completed)
		if err != nil {
			return err
		}
		if !retry {
			break
		}
	}
	return nil
}

func waitForPodCompletionOrTimeout(podClient coreclientset.PodInterface, name string, completed map[string]time.Time) (bool, error) {
	list, err := podClient.List(meta.ListOptions{FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String()})
	if err != nil {
		return false, err
	}
	if len(list.Items) != 1 {
		return false, fmt.Errorf("pod %s was already deleted", name)
	}
	pod := &list.Items[0]
	if pod.Spec.RestartPolicy == coreapi.RestartPolicyAlways {
		return false, nil
	}
	podLogNewFailedContainers(podClient, pod, completed)
	if podJobIsOK(pod) {
		log.Printf("Pod %s already succeeded in %s", pod.Name, podDuration(pod))
		return false, nil
	}
	if podJobIsFailed(pod) {
		return false, fmt.Errorf("the pod %s/%s failed after %s (failed containers: %s)", pod.Namespace, pod.Name, podDuration(pod), strings.Join(namesFromMap(completed), ", "))
	}

	watcher, err := podClient.Watch(meta.ListOptions{
		FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String(),
		Watch:         true,
	})
	if err != nil {
		return false, err
	}
	defer watcher.Stop()

	for {
		event, ok := <-watcher.ResultChan()
		if !ok {
			// restart
			return true, nil
		}
		if pod, ok := event.Object.(*coreapi.Pod); ok {
			podLogNewFailedContainers(podClient, pod, completed)
			if podJobIsOK(pod) {
				log.Printf("Pod %s succeeded after %s", pod.Name, podDuration(pod))
				return false, nil
			}
			if podJobIsFailed(pod) {
				return false, fmt.Errorf("the pod %s/%s failed after %s (failed containers: %s)", pod.Namespace, pod.Name, podDuration(pod), strings.Join(namesFromMap(completed), ", "))
			}
		}
		if event.Type == watch.Deleted {
			podLogNewFailedContainers(podClient, pod, completed)
			return false, fmt.Errorf("the pod %s/%s was deleted without completing after %s (failed containers: %s)", pod.Namespace, pod.Name, podDuration(pod), strings.Join(namesFromMap(completed), ", "))
		}
	}
}

func podDuration(pod *coreapi.Pod) time.Duration {
	start := pod.Status.StartTime
	if start == nil {
		start = &pod.CreationTimestamp
	}
	var end meta.Time
	for _, status := range pod.Status.ContainerStatuses {
		if s := status.State.Terminated; s != nil {
			if end.IsZero() || s.FinishedAt.Time.Before(end.Time) {
				end = s.FinishedAt
			}
		}
	}
	if end.IsZero() {
		for _, status := range pod.Status.InitContainerStatuses {
			if s := status.State.Terminated; s != nil && s.ExitCode != 0 {
				if end.IsZero() || s.FinishedAt.Time.Before(end.Time) {
					end = s.FinishedAt
					break
				}
			}
		}
	}
	if end.IsZero() {
		end = meta.Now()
	}
	duration := end.Sub(start.Time)
	return duration
}

func templateInstanceReady(instance *templateapi.TemplateInstance) (ready bool, err error) {
	for _, c := range instance.Status.Conditions {
		switch {
		case c.Type == templateapi.TemplateInstanceReady && c.Status == coreapi.ConditionTrue:
			return true, nil
		case c.Type == templateapi.TemplateInstanceInstantiateFailure && c.Status == coreapi.ConditionTrue:
			return true, fmt.Errorf("failed to create objects: %s", c.Message)
		}
	}
	return false, nil
}

func podJobIsOK(p *coreapi.Pod) bool {
	return p.Status.Phase == coreapi.PodSucceeded
}

func podJobIsFailed(p *coreapi.Pod) bool {
	if p.Status.Phase == coreapi.PodFailed {
		return true
	}
	for _, status := range p.Status.InitContainerStatuses {
		if s := status.State.Terminated; s != nil {
			if s.ExitCode != 0 {
				return true
			}
		}
	}
	for _, status := range p.Status.ContainerStatuses {
		if s := status.State.Terminated; s != nil {
			if s.ExitCode != 0 {
				return true
			}
		}
	}
	return false
}

func namesFromMap(completed map[string]time.Time) []string {
	var names []string
	for k := range completed {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func podLogNewFailedContainers(podClient coreclientset.PodInterface, pod *coreapi.Pod, completed map[string]time.Time) {
	var statuses []coreapi.ContainerStatus
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)

	for _, status := range statuses {
		if _, ok := completed[status.Name]; ok {
			continue
		}
		s := status.State.Terminated
		if s == nil || s.ExitCode == 0 {
			continue
		}
		completed[status.Name] = s.FinishedAt.Time

		if s, err := podClient.GetLogs(pod.Name, &coreapi.PodLogOptions{
			Container: status.Name,
		}).Stream(); err == nil {
			log.Printf("Pod %s container %s failed, exit code %d:", pod.Name, status.Name, status.State.Terminated.ExitCode)
			if _, err := io.Copy(os.Stdout, s); err != nil {
				log.Printf("error: Unable to copy log output from failed pod container %s: %v", status.Name, err)
			}
			s.Close()
		} else {
			log.Printf("error: Unable to retrieve logs from failed pod container %s: %v", status.Name, err)
		}
	}
}
