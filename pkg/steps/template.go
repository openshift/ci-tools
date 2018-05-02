package steps

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
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
	podClient      coreclientset.PodInterface
	jobSpec        *JobSpec
}

func (s *templateExecutionStep) Run(dry bool) error {
	log.Printf("Executing template %s in %s", s.template.Name, s.jobSpec.Namespace())

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
				return fmt.Errorf("cannot resolve parameter %s into template %s: %v", p.Name, s.template.Name, err)
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
				s.template.Parameters[i].Value = strings.Replace(format, "${component}", component, -1)
			}
		}
	}

	if dry {
		j, _ := json.MarshalIndent(s.template, "", "  ")
		log.Printf("template:\n%s", j)
		return nil
	}

	instance, err := s.templateClient.TemplateInstances(s.jobSpec.Namespace()).Create(&templateapi.TemplateInstance{
		ObjectMeta: meta.ObjectMeta{
			Name: s.template.Name,
		},
		Spec: templateapi.TemplateInstanceSpec{
			Template: *s.template,
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("unable to process template: %v", err)
	}
	for {
		if instance != nil {
			ready, err := templateInstanceReady(instance)
			if err != nil {
				return err
			}
			if ready {
				break
			}
			time.Sleep(2 * time.Second)
		}
		instance, err = s.templateClient.TemplateInstances(s.jobSpec.Namespace()).Get(s.template.Name, meta.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to retrieve existing template: %v", err)
		}
	}

	for _, ref := range instance.Status.Objects {
		switch {
		case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
			log.Printf("Running pod %s/%s", ref.Ref.Namespace, ref.Ref.Name)
		}
	}
	for _, ref := range instance.Status.Objects {
		switch {
		case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
			if err := waitForPodCompletion(s.podClient, ref.Ref.Name); err != nil {
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
			ready, err := isPodCompleted(s.podClient, ref.Ref.Name)
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

func TemplateExecutionStep(template *templateapi.Template, params *DeferredParameters, podClient coreclientset.PodInterface, templateClient TemplateClient, jobSpec *JobSpec) api.Step {
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
	return len(os.Getenv(name)) > 0
}

func (p *DeferredParameters) Links(name string) []api.StepLink {
	p.lock.Lock()
	defer p.lock.Unlock()
	if len(os.Getenv(name)) > 0 {
		return nil
	}
	return p.links[name]
}

func (p *DeferredParameters) AllLinks() []api.StepLink {
	p.lock.Lock()
	defer p.lock.Unlock()
	var links []api.StepLink
	for name, v := range p.links {
		if len(os.Getenv(name)) > 0 {
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
	if value := os.Getenv(name); len(value) > 0 {
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
	Process(template *templateapi.Template) (*templateapi.Template, error)
}

type templateClient struct {
	templateclientset.TemplateV1Interface
	restClient rest.Interface
	namespace  string
}

func NewTemplateClient(client templateclientset.TemplateV1Interface, restClient rest.Interface, namespace string) TemplateClient {
	return &templateClient{
		TemplateV1Interface: client,
		restClient:          restClient,
		namespace:           namespace,
	}
}

func (c *templateClient) Process(template *templateapi.Template) (*templateapi.Template, error) {
	processed := &templateapi.Template{}
	err := c.restClient.Post().
		Namespace(c.namespace).
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

func waitForPodCompletion(podClient coreclientset.PodInterface, name string) error {
	for {
		retry, err := waitForPodCompletionOrTimeout(podClient, name)
		if err != nil {
			return err
		}
		if !retry {
			break
		}
	}
	return nil
}

func waitForPodCompletionOrTimeout(podClient coreclientset.PodInterface, name string) (bool, error) {
	isOK := func(p *coreapi.Pod) bool {
		return p.Status.Phase == coreapi.PodSucceeded
	}
	isFailed := func(p *coreapi.Pod) bool {
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
	if isOK(pod) {
		log.Printf("Pod %s/%s already succeeded in %s", pod.Namespace, pod.Name, podDuration(pod))
		return false, nil
	}
	if isFailed(pod) {
		printFailedPodLogs(podClient, pod)
		return false, fmt.Errorf("the pod %s/%s failed with status %q", pod.Namespace, pod.Name, pod.Status.Phase)
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
			if isOK(pod) {
				log.Printf("Pod %s/%s succeeded after %s", pod.Namespace, pod.Name, podDuration(pod))
				return false, nil
			}
			if isFailed(pod) {
				printFailedPodLogs(podClient, pod)
				return false, fmt.Errorf("the pod %s/%s failed after %s with status %q", pod.Namespace, pod.Name, podDuration(pod), pod.Status.Phase)
			}
		}
		if event.Type == watch.Deleted {
			printFailedPodLogs(podClient, pod)
			return false, fmt.Errorf("the pod %s/%s was deleted without completing after %s with status %q", pod.Namespace, pod.Name, podDuration(pod), pod.Status.Phase)
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

func printFailedPodLogs(podClient coreclientset.PodInterface, pod *coreapi.Pod) {
	var statuses []coreapi.ContainerStatus
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)

	for _, status := range statuses {
		if status.State.Terminated == nil || status.State.Terminated.ExitCode == 0 {
			continue
		}
		if s, err := podClient.GetLogs(pod.Name, &coreapi.PodLogOptions{
			Container: status.Name,
		}).Stream(); err == nil {
			log.Printf("Pod %s/%s container %s failed, exit code %d:", pod.Namespace, pod.Name, status.Name, status.State.Terminated.ExitCode)
			if _, err := io.Copy(os.Stdout, s); err != nil {
				log.Printf("error: Unable to copy log output from failed pod container %s: %v", status.Name, err)
			}
			s.Close()
		} else {
			log.Printf("error: Unable to retrieve logs from failed pod container %s: %v", status.Name, err)
		}
	}
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
