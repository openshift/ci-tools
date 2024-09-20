package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/util"
)

var (
	coreScheme   = runtime.NewScheme()
	corev1Codec  = codecFactory.LegacyCodec(coreapi.SchemeGroupVersion)
	codecFactory = serializer.NewCodecFactory(coreScheme)
)

func init() {
	utilruntime.Must(coreapi.AddToScheme(coreScheme))
}

const (
	RefsOrgLabel            = "ci.openshift.io/refs.org"
	RefsRepoLabel           = "ci.openshift.io/refs.repo"
	RefsBranchLabel         = "ci.openshift.io/refs.branch"
	RefsVariantLabel        = "ci.openshift.io/refs.variant"
	JobNameLabel            = "ci.openshift.io/job"
	MultiStageStepNameLabel = "ci.openshift.io/step"

	TestContainerName = "test"
)

type templateExecutionStep struct {
	template  *templateapi.Template
	resources api.ResourceConfiguration
	params    api.Parameters
	podClient kubernetes.PodClient
	client    TemplateClient
	jobSpec   *api.JobSpec

	subTests []*junit.TestCase
}

func (s *templateExecutionStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*templateExecutionStep) Validate() error { return nil }

func (s *templateExecutionStep) Run(ctx context.Context) error {
	return results.ForReason("executing_template").ForError(s.run(ctx))
}

func (s *templateExecutionStep) run(ctx context.Context) error {
	logrus.Infof("Executing template %s", s.template.Name)

	if len(s.template.Objects) == 0 {
		return fmt.Errorf("template %s has no objects", s.template.Name)
	}

	for i, p := range s.template.Parameters {
		if len(p.Value) == 0 {
			if !s.params.Has(p.Name) && !utils.IsStableImageEnv(p.Name) && p.Required {
				return fmt.Errorf("template %s has required parameter %s which is not defined", s.template.Name, p.Name)
			}
		}
		if s.params.Has(p.Name) {
			value, err := s.params.Get(p.Name)
			if err != nil {
				return fmt.Errorf("cannot resolve parameter %s into template %s: %w", p.Name, s.template.Name, err)
			}
			if len(value) > 0 {
				s.template.Parameters[i].Value = value
			}
			continue
		}
		if utils.IsStableImageEnv(p.Name) {
			component := utils.StableImageNameFrom(p.Name)
			format, err := s.params.Get(utils.ImageFormatEnv)
			if err != nil {
				return fmt.Errorf("could not resolve image format: %w", err)
			}
			s.template.Parameters[i].Value = strings.Replace(format, api.ComponentFormatReplacement, component, -1)
		}
	}

	operateOnTemplatePods(s.template, s.resources)
	injectLabelsToTemplate(s.jobSpec, s.template)

	// TODO: enforce single namespace behavior
	instance := &templateapi.TemplateInstance{
		ObjectMeta: meta.ObjectMeta{
			Namespace: s.jobSpec.Namespace(),
			Name:      s.template.Name,
		},
		Spec: templateapi.TemplateInstanceSpec{
			Template: *s.template,
		},
	}
	if owner := s.jobSpec.Owner(); owner != nil {
		instance.OwnerReferences = append(instance.OwnerReferences, *owner)
	}

	go func() {
		<-ctx.Done()
		logrus.Infof("cleanup: Deleting template %s", s.template.Name)
		if err := s.client.Delete(CleanupCtx, &templateapi.TemplateInstance{ObjectMeta: meta.ObjectMeta{Namespace: s.jobSpec.Namespace(), Name: s.template.Name}}, ctrlruntimeclient.PropagationPolicy(meta.DeletePropagationForeground)); err != nil && !kerrors.IsNotFound(err) {
			logrus.WithError(err).Error("Could not delete template instance.")
		}
	}()

	logrus.Debugf("Creating or restarting template instance")
	_, err := createOrRestartTemplateInstance(ctx, s.client, instance)
	if err != nil {
		return fmt.Errorf("could not create or restart template instance: %w", err)
	}

	logrus.Debugf("Waiting for template instance to be ready")
	instance, err = waitForTemplateInstanceReady(ctrlruntimeclient.NewNamespacedClient(s.client, s.jobSpec.Namespace()), s.template.Name)
	if err != nil {
		return fmt.Errorf("could not wait for template instance to be ready: %w", err)
	}

	// now that the pods have been resolved by the template, add them to the artifact map
	var notifier util.ContainerNotifier = util.NopNotifier
	if artifactDir, artifactsRequested := api.Artifacts(); artifactsRequested {
		artifacts := NewArtifactWorker(s.podClient, filepath.Join(artifactDir, s.template.Name), s.jobSpec.Namespace())
		for _, ref := range instance.Status.Objects {
			switch {
			case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
				pod := &coreapi.Pod{}
				if err := s.podClient.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: ref.Ref.Name}, pod); err != nil {
					return fmt.Errorf("unable to retrieve pod from template - possibly deleted: %w", err)
				}
				addArtifactContainersFromPod(pod, artifacts)
			}
		}
		notifier = artifacts
	}

	for _, ref := range instance.Status.Objects {
		switch {
		case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
			logrus.Debugf("Running pod %s", ref.Ref.Name)
		}
	}

	testCaseNotifier := NewTestCaseNotifier(notifier)
	for _, ref := range instance.Status.Objects {
		switch {
		case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
			_, err := util.WaitForPodCompletion(context.TODO(), s.podClient, s.jobSpec.Namespace(), ref.Ref.Name, testCaseNotifier, util.WaitForPodFlag(0))
			s.subTests = append(s.subTests, testCaseNotifier.SubTests(fmt.Sprintf("%s - %s ", s.Description(), ref.Ref.Name))...)
			if err != nil {
				return fmt.Errorf("template pod %q failed: %w", ref.Ref.Name, err)
			}
		}
	}
	// TODO properly identify deleted templates in waitForPodCompletion
	select {
	case <-ctx.Done():
		return fmt.Errorf("template test cancelled")
	default:
		break
	}
	return nil
}

func injectLabelsToTemplate(jobSpec *api.JobSpec, template *templateapi.Template) {
	if refs := jobSpec.JobSpec.Refs; refs != nil {
		if template.ObjectLabels == nil {
			template.ObjectLabels = make(map[string]string)
		}
		template.ObjectLabels[RefsOrgLabel] = refs.Org
		template.ObjectLabels[RefsRepoLabel] = refs.Repo
		template.ObjectLabels[RefsBranchLabel] = refs.BaseRef
	}
}

func hasTestContainerWithResources(pod *coreapi.Pod) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == TestContainerName && hasContainerResources(container) {
			return true
		}
	}
	return false
}

func hasContainerResources(container coreapi.Container) bool {
	resources := container.Resources
	return len(resources.Limits) > 0 || len(resources.Requests) > 0
}

func injectResourcesToPod(pod *coreapi.Pod, templateName string, resources api.ResourceConfiguration) error {
	containerResources, err := ResourcesFor(resources.RequirementsForStep(templateName))
	if err != nil {
		return fmt.Errorf("unable to calculate resources for %s: %w", pod.Name, err)
	}

	for index, container := range pod.Spec.Containers {
		if container.Name == TestContainerName {
			pod.Spec.Containers[index].Resources = containerResources
			break
		}
	}

	return nil
}

func operateOnTemplatePods(template *templateapi.Template, resources api.ResourceConfiguration) {
	for index, object := range template.Objects {
		if pod := getPodFromObject(object); pod != nil {
			addArtifactsToPod(pod)

			if resources != nil && !hasTestContainerWithResources(pod) {
				if err := injectResourcesToPod(pod, template.Name, resources); err != nil {
					logrus.WithError(err).Warn("Couldn't inject resources to pod.")
				}
			}

			template.Objects[index].Raw = []byte(runtime.EncodeOrDie(corev1Codec, pod))
			template.Objects[index].Object = pod.DeepCopyObject()
		}
	}
}

func (s *templateExecutionStep) SubTests() []*junit.TestCase {
	return s.subTests
}

func (s *templateExecutionStep) Requires() []api.StepLink {
	var links []api.StepLink
	for _, p := range s.template.Parameters {
		if link, ok := utils.LinkForEnv(p.Name); ok {
			links = append(links, link)
		}
	}
	return links
}

func (s *templateExecutionStep) Creates() []api.StepLink {
	return []api.StepLink{}
}

func (s *templateExecutionStep) Provides() api.ParameterMap {
	return nil
}

func (s *templateExecutionStep) Name() string { return s.template.Name }

func (s *templateExecutionStep) Description() string {
	return fmt.Sprintf("Run template %s", s.template.Name)
}

func (s *templateExecutionStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func (s *templateExecutionStep) IsMultiArch() bool           { return false }
func (s *templateExecutionStep) SetMultiArch(multiArch bool) {}

func TemplateExecutionStep(template *templateapi.Template, params api.Parameters, podClient kubernetes.PodClient, templateClient TemplateClient, jobSpec *api.JobSpec, resources api.ResourceConfiguration) api.Step {
	return &templateExecutionStep{
		template:  template,
		resources: resources,
		params:    params,
		podClient: podClient,
		client:    templateClient,
		jobSpec:   jobSpec,
	}
}

type TemplateClient interface {
	loggingclient.LoggingClient
	Process(namespace string, template *templateapi.Template) (*templateapi.Template, error)
}

type templateClient struct {
	loggingclient.LoggingClient
	restClient rest.Interface
}

func NewTemplateClient(client loggingclient.LoggingClient, restClient rest.Interface) TemplateClient {
	return &templateClient{
		LoggingClient: client,
		restClient:    restClient,
	}
}

func (c *templateClient) Process(namespace string, template *templateapi.Template) (*templateapi.Template, error) {
	processed := &templateapi.Template{}
	err := c.restClient.Post().
		Namespace(namespace).
		Resource("processedtemplates").
		Body(template).
		Do(context.TODO()).
		Into(processed)
	return processed, fmt.Errorf("could not process template: %w", err)
}

func waitForTemplateInstanceReady(client ctrlruntimeclient.Client, name string) (*templateapi.TemplateInstance, error) {
	instance := &templateapi.TemplateInstance{}
	err := wait.PollImmediate(2*time.Second, 10*time.Minute, func() (bool, error) {
		if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Name: name}, instance); err != nil {
			return false, err
		}

		return templateInstanceReady(instance)
	})

	return instance, err
}

func createOrRestartTemplateInstance(ctx context.Context, client ctrlruntimeclient.Client, instance *templateapi.TemplateInstance) (*templateapi.TemplateInstance, error) {
	namespace, name := instance.Namespace, instance.Name
	if err := waitForCompletedTemplateInstanceDeletion(ctx, client, namespace, name); err != nil {
		return nil, fmt.Errorf("unable to delete completed template instance: %w", err)
	}
	err := client.Create(ctx, instance)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("unable to create template instance: %w", err)
	}
	if err != nil {
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: instance.Name}, instance); err != nil {
			return nil, fmt.Errorf("unable to retrieve pod: %w", err)
		}
		logrus.Infof("Waiting for running template %s to finish", instance.Name)
	}
	return instance, nil
}

func waitForCompletedTemplateInstanceDeletion(ctx context.Context, client ctrlruntimeclient.Client, namespace, name string) error {
	instance := &templateapi.TemplateInstance{}
	err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, instance)
	if kerrors.IsNotFound(err) {
		logrus.Debugf("Template instance %s already deleted, do not need to wait any longer", name)
		return nil
	}

	// delete the instance we had before, otherwise another user has relaunched this template
	uid := instance.UID
	policy := meta.DeletePropagationForeground
	opts := &ctrlruntimeclient.DeleteOptions{Raw: &meta.DeleteOptions{
		PropagationPolicy: &policy,
		Preconditions:     &meta.Preconditions{UID: &uid},
	}}
	err = client.Delete(ctx, instance, opts)
	if kerrors.IsNotFound(err) {
		logrus.Infof("After initial existence check, a delete of template %s and instance %s received a not found error ",
			name, string(instance.UID))
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not delete completed template instance: %w", err)
	}

	for i := 0; ; i++ {
		err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, instance)
		if kerrors.IsNotFound(err) {
			break
		}
		if err != nil {
			return fmt.Errorf("could not retrieve deleting template instance: %w", err)
		}
		if instance.UID != uid {
			return nil
		}
		if i == 1800 {
			data, _ := json.MarshalIndent(instance.Status, "", "  ")
			logrus.Infof("Template instance %s has not completed deletion after 30 minutes, possible error in controller:\n%s", name, string(data))
		}

		logrus.Debugf("Waiting for template instance %s to be deleted ...", name)
		time.Sleep(2 * time.Second)
	}

	// TODO: we have to wait for all pods because graceful deletion foreground isn't working on template instance
	for _, ref := range instance.Status.Objects {
		switch {
		case ref.Ref.Kind == "Pod" && ref.Ref.APIVersion == "v1":
			if err := util.WaitForPodDeletion(ctx, client, namespace, ref.Ref.Name, ref.Ref.UID); err != nil {
				return err
			}
		}
	}
	return nil
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

func getPodFromObject(object runtime.RawExtension) *coreapi.Pod {
	// We don't care for errors, because we accept that this func() will check also a non-pod objects.
	requiredObj, _ := runtime.Decode(codecFactory.UniversalDecoder(coreapi.SchemeGroupVersion), object.Raw)
	if pod, ok := requiredObj.(*coreapi.Pod); ok {
		return pod
	}

	return nil
}
