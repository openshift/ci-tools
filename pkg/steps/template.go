package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
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
	podClient PodClient
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
		if err := s.client.Delete(cleanupCtx, &templateapi.TemplateInstance{ObjectMeta: meta.ObjectMeta{Namespace: s.jobSpec.Namespace(), Name: s.template.Name}}, ctrlruntimeclient.PropagationPolicy(meta.DeletePropagationForeground)); err != nil && !kerrors.IsNotFound(err) {
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
	var notifier ContainerNotifier = NopNotifier
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
			_, err := waitForPodCompletion(context.TODO(), s.podClient, s.jobSpec.Namespace(), ref.Ref.Name, testCaseNotifier, false)
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
	containerResources, err := resourcesFor(resources.RequirementsForStep(templateName))
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

func TemplateExecutionStep(template *templateapi.Template, params api.Parameters, podClient PodClient, templateClient TemplateClient, jobSpec *api.JobSpec, resources api.ResourceConfiguration) api.Step {
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
			if err := waitForPodDeletion(ctx, client, namespace, ref.Ref.Name, ref.Ref.UID); err != nil {
				return err
			}
		}
	}
	return nil
}

func createOrRestartPod(ctx context.Context, podClient ctrlruntimeclient.Client, pod *coreapi.Pod) (*coreapi.Pod, error) {
	namespace, name := pod.Namespace, pod.Name
	if err := waitForCompletedPodDeletion(ctx, podClient, namespace, name); err != nil {
		return nil, fmt.Errorf("unable to delete completed pod: %w", err)
	}
	if pod.Spec.ActiveDeadlineSeconds == nil {
		logrus.Debugf("Executing pod %q running image %q", pod.Name, pod.Spec.Containers[0].Image)
	} else {
		logrus.Debugf("Executing pod %q with activeDeadlineSeconds=%d", pod.Name, *pod.Spec.ActiveDeadlineSeconds)
	}
	// creating a pod in close proximity to namespace creation can result in forbidden errors due to
	// initializing secrets or policy - use a short backoff to mitigate flakes
	if err := wait.ExponentialBackoff(wait.Backoff{Steps: 4, Factor: 2, Duration: time.Second}, func() (bool, error) {
		err := podClient.Create(ctx, pod)
		if err != nil {
			if kerrors.IsForbidden(err) {
				logrus.WithError(err).Warnf("Unable to create pod %s, may be temporary.", name)
				return false, nil
			}
			if !kerrors.IsAlreadyExists(err) {
				return false, err
			}

			if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod); err != nil {
				return false, err
			}
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("unable to create pod: %w", err)
	}
	return pod, nil
}

func waitForPodDeletion(ctx context.Context, podClient ctrlruntimeclient.Client, namespace, name string, uid types.UID) error {
	return wait.ExponentialBackoffWithContext(ctx, wait.Backoff{Duration: 2 * time.Second, Factor: 2, Steps: 10}, func() (done bool, err error) {
		pod := &coreapi.Pod{}
		err = podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod)
		if kerrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return true, fmt.Errorf("could not retrieve deleting pod: %w", err)
		}
		if pod.UID != uid {
			return true, nil
		}
		logrus.Debugf("Waiting for pod %s to be deleted ...", name)
		return false, nil
	})
}

func waitForCompletedPodDeletion(ctx context.Context, podClient ctrlruntimeclient.Client, namespace, name string) error {
	pod := &coreapi.Pod{}
	if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod); kerrors.IsNotFound(err) {
		return nil
	}
	// running pods are left to run, we just wait for them to finish
	if pod.Status.Phase != coreapi.PodSucceeded && pod.Status.Phase != coreapi.PodFailed && pod.DeletionTimestamp == nil {
		return nil
	}

	// delete the pod we expect, otherwise another user has relaunched this pod
	uid := pod.UID
	err := podClient.Delete(ctx, pod, ctrlruntimeclient.Preconditions(meta.Preconditions{UID: &uid}))
	if kerrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not delete completed pod: %w", err)
	}

	return waitForPodDeletion(ctx, podClient, namespace, name, uid)
}

func waitForPodCompletion(ctx context.Context, podClient PodClient, namespace, name string, notifier ContainerNotifier, skipLogs bool) (*coreapi.Pod, error) {
	if notifier == nil {
		notifier = NopNotifier
	}
	ctxDone := ctx.Done()
	notifierDone := notifier.Done(name)
	completed := make(map[string]time.Time)
	var pod *coreapi.Pod
	for {
		newPod, err := waitForPodCompletionOrTimeout(ctx, podClient, namespace, name, completed, notifier, skipLogs)
		if newPod != nil {
			pod = newPod
		}
		// continue waiting if the container notifier is not yet complete for the given pod
		select {
		case <-notifierDone:
		case <-ctxDone:
		default:
			skipLogs = true
			if err == nil {
				select {
				case <-notifierDone:
				case <-ctxDone:
				case <-time.After(5 * time.Second):
				}
			}
			continue
		}
		if err != nil {
			return pod, err
		}
		break
	}
	return pod, nil
}

func waitForPodCompletionOrTimeout(ctx context.Context, podClient PodClient, namespace, name string, completed map[string]time.Time, notifier ContainerNotifier, skipLogs bool) (*coreapi.Pod, error) {
	// Warning: this is extremely fragile, inherited legacy code.  Please be
	// careful and test thoroughly when making changes, as they very frequently
	// lead to systemic production failures.  Some guidance:
	// - There is a complex interaction between this code and the container
	//   notifier.  Updates to the state of the pod are received via the watch
	//   and communicated to the notifier.  Even in case of interruption (i.e.
	//   cancellation of `ctx`) and/or failure, events should continue to be
	//   processed until the notifier signals that it is done.  This ensures
	//   the state of the pod is correctly reported, artifacts are gathered,
	//   and termination happens deterministically for both success and failure
	//   scenarios.
	// - Since ea8f62fcf, most of the above only applies to template tests.
	//   Container and multi-stage tests now solely rely on `test-infra`'s
	//   `pod-utils` for artifact gathering and so use a notifier which
	//   instantly reports itself as done when the watched containers finish.
	pod := &coreapi.Pod{}
	if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod); err != nil {
		if kerrors.IsNotFound(err) {
			notifier.Complete(name)
			logrus.Infof("error: could not wait for pod '%s': it is no longer present on the cluster"+
				" (usually a result of a race or resource pressure. re-running the job should help)", name)
			return nil, fmt.Errorf("pod was deleted while ci-operator step was waiting for it")
		}
		return nil, fmt.Errorf("could not list pod: %w", err)
	}

	if pod.Spec.RestartPolicy == coreapi.RestartPolicyAlways {
		return pod, nil
	}
	podLogNewFailedContainers(podClient, pod, completed, notifier, skipLogs)
	if podJobIsOK(pod) {
		if !skipLogs {
			logrus.Debugf("Pod %s already succeeded in %s", pod.Name, podDuration(pod).Truncate(time.Second))
		}
		return pod, nil
	}
	if podJobIsFailed(pod) {
		return pod, appendLogToError(fmt.Errorf("the pod %s/%s failed after %s (failed containers: %s): %s", pod.Namespace, pod.Name, podDuration(pod).Truncate(time.Second), strings.Join(failedContainerNames(pod), ", "), podReason(pod)), podMessages(pod))
	}
	done := ctx.Done()

	podCheckTicker := time.NewTicker(10 * time.Second)
	defer podCheckTicker.Stop()
	podStartTimeout := 30 * time.Minute
	var podSeenRunning bool

	for {
		select {
		case <-done:
			return pod, ctx.Err()
		case <-podCheckTicker.C:
			if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod); err != nil {
				if kerrors.IsNotFound(err) {
					return pod, appendLogToError(fmt.Errorf("the pod %s/%s was deleted without completing after %s (failed containers: %s)", pod.Namespace, pod.Name, podDuration(pod).Truncate(time.Second), strings.Join(failedContainerNames(pod), ", ")), podMessages(pod))
				}
				logrus.WithError(err).Warnf("Failed to get pod %s.", name)
				continue
			}

			if !podSeenRunning {
				if podHasStarted(pod) {
					podSeenRunning = true
				} else if time.Since(pod.CreationTimestamp.Time) > podStartTimeout {
					message := fmt.Sprintf("pod didn't start running within %s: %s\n%s", podStartTimeout, getReasonsForUnreadyContainers(pod), getEventsForPod(ctx, pod, podClient))
					logrus.Infof(message)
					notifier.Complete(name)
					return pod, errors.New(message)
				}
			}
			podLogNewFailedContainers(podClient, pod, completed, notifier, skipLogs)
			if podJobIsOK(pod) {
				if !skipLogs {
					logrus.Debugf("Pod %s succeeded after %s", pod.Name, podDuration(pod).Truncate(time.Second))
				}
				return pod, nil
			}
			if podJobIsFailed(pod) {
				return pod, appendLogToError(fmt.Errorf("the pod %s/%s failed after %s (failed containers: %s): %s", pod.Namespace, pod.Name, podDuration(pod).Truncate(time.Second), strings.Join(failedContainerNames(pod), ", "), podReason(pod)), podMessages(pod))
			}
		}
	}
}

// podHasStarted checks if a test pod can be considered as "running".
// Init containers are also checked because they can be declared in template
// tests, but those added by the test infrastructure are ignored.
func podHasStarted(pod *coreapi.Pod) bool {
	if pod.Status.Phase == coreapi.PodRunning {
		return true
	}
	// Status is still `Pending` while init containers are executed.
	for _, s := range pod.Status.InitContainerStatuses {
		if s.Name != "cp-secret-wrapper" && s.State.Running != nil {
			return true
		}
	}
	return false
}

// podReason returns the pod's reason and message for exit or tries to find one from the pod.
func podReason(pod *coreapi.Pod) string {
	reason := pod.Status.Reason
	message := pod.Status.Message
	if len(reason) == 0 {
		reason = "ContainerFailed"
	}
	if len(message) == 0 {
		message = "one or more containers exited"
	}
	return fmt.Sprintf("%s %s", reason, message)
}

// podMessages returns a string containing the messages and reasons for all terminated containers with a non-zero exit code.
func podMessages(pod *coreapi.Pod) string {
	var messages []string
	for _, status := range append(append([]coreapi.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if state := status.State.Terminated; state != nil && state.ExitCode != 0 {
			messages = append(messages, fmt.Sprintf("Container %s exited with code %d, reason %s", status.Name, state.ExitCode, state.Reason))
			if msg := strings.TrimSpace(state.Message); len(msg) > 0 {
				messages = append(messages, "---", msg, "---")
			}
		}
	}
	return strings.Join(messages, "\n")
}

func podDuration(pod *coreapi.Pod) time.Duration {
	start := pod.Status.StartTime
	if start == nil {
		start = &pod.CreationTimestamp
	}
	var end meta.Time
	for _, status := range pod.Status.ContainerStatuses {
		if s := status.State.Terminated; s != nil {
			if end.IsZero() || s.FinishedAt.Time.After(end.Time) {
				end = s.FinishedAt
			}
		}
	}
	if end.IsZero() {
		for _, status := range pod.Status.InitContainerStatuses {
			if s := status.State.Terminated; s != nil && s.ExitCode != 0 {
				if end.IsZero() || s.FinishedAt.Time.After(end.Time) {
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

func podRunningContainers(pod *coreapi.Pod) []string {
	var names []string
	for _, status := range append(append([]coreapi.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if status.State.Running != nil || status.State.Waiting != nil || status.State.Terminated == nil {
			continue
		}
		names = append(names, status.Name)
	}
	return names
}

func podJobIsOK(pod *coreapi.Pod) bool {
	if pod.Status.Phase == coreapi.PodSucceeded {
		return true
	}
	if pod.Status.Phase == coreapi.PodPending || pod.Status.Phase == coreapi.PodUnknown {
		return false
	}
	// if all containers except artifacts are in terminated and have exit code 0, we're ok
	hasArtifacts := false
	for _, status := range append(append([]coreapi.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		// don't succeed until everything has started at least once
		if status.State.Waiting != nil && status.LastTerminationState.Terminated == nil {
			return false
		}
		if status.Name == "artifacts" {
			hasArtifacts = true
			continue
		}
		s := status.State.Terminated
		if s == nil {
			return false
		}
		if s.ExitCode != 0 {
			return false
		}
	}
	if pod.Status.Phase == coreapi.PodFailed && !hasArtifacts {
		return false
	}
	return true
}

func podJobIsFailed(pod *coreapi.Pod) bool {
	if pod.Status.Phase == coreapi.PodFailed {
		return true
	}
	if pod.Status.Phase == coreapi.PodPending || pod.Status.Phase == coreapi.PodUnknown {
		return false
	}
	// if any container is in a non-zero status we have failed
	for _, status := range append(append([]coreapi.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		// don't fail until everything has started at least once
		if status.State.Waiting != nil && status.LastTerminationState.Terminated == nil {
			return false
		}
		if status.Name == "artifacts" {
			continue
		}
		if s := status.State.Terminated; s != nil {
			if s.ExitCode != 0 {
				return true
			}
		}
	}
	return false
}

func failedContainerNames(pod *coreapi.Pod) []string {
	var names []string
	for _, status := range append(append([]coreapi.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if s := status.State.Terminated; s != nil {
			if s.ExitCode != 0 {
				names = append(names, status.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func podLogNewFailedContainers(podClient PodClient, pod *coreapi.Pod, completed map[string]time.Time, notifier ContainerNotifier, skipLogs bool) {
	var statuses []coreapi.ContainerStatus
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)

	for _, status := range statuses {
		if _, ok := completed[status.Name]; ok {
			continue
		}
		s := status.State.Terminated
		if s == nil {
			continue
		}
		completed[status.Name] = s.FinishedAt.Time
		notifier.Notify(pod, status.Name)

		if s.ExitCode == 0 {
			if !skipLogs {
				logrus.Debugf("Container %s in pod %s completed successfully", status.Name, pod.Name)
			}
			continue
		}

		if s, err := podClient.GetLogs(pod.Namespace, pod.Name, &coreapi.PodLogOptions{
			Container: status.Name,
		}).Stream(context.TODO()); err == nil {
			logs := &bytes.Buffer{}
			if _, err := io.Copy(logs, s); err != nil {
				logrus.WithError(err).Warnf("Unable to copy log output from failed pod container %s.", status.Name)
			}
			if err := s.Close(); err != nil {
				logrus.WithError(err).Warnf("Unable to close log output from failed pod container %s.", status.Name)
			}
			logrus.Infof("Logs for container %s in pod %s:", status.Name, pod.Name)
			logrus.Info(logs.String())
		} else {
			logrus.WithError(err).Warnf("error: Unable to retrieve logs from failed pod container %s.", status.Name)
		}

		logrus.Debugf("Container %s in pod %s failed, exit code %d, reason %s", status.Name, pod.Name, status.State.Terminated.ExitCode, status.State.Terminated.Reason)
	}
	// Workaround for https://github.com/kubernetes/kubernetes/issues/88611
	// Pods may be terminated with DeadlineExceeded with spec.ActiveDeadlineSeconds is set.
	// However this doesn't change container statuses, so len(podRunningContainers(pod) is never 0.
	// Notify the test is complete if ActiveDeadlineSeconds is set and pod has failed.
	if pod.Status.Phase == coreapi.PodFailed && pod.Spec.ActiveDeadlineSeconds != nil {
		notifier.Complete(pod.Name)
	}
	// if there are no running containers and we're in a terminal state, mark the pod complete
	if (pod.Status.Phase == coreapi.PodFailed || pod.Status.Phase == coreapi.PodSucceeded) && len(podRunningContainers(pod)) == 0 {
		notifier.Complete(pod.Name)
	}
}

func getPodFromObject(object runtime.RawExtension) *coreapi.Pod {
	// We don't care for errors, because we accept that this func() will check also a non-pod objects.
	requiredObj, _ := runtime.Decode(codecFactory.UniversalDecoder(coreapi.SchemeGroupVersion), object.Raw)
	if pod, ok := requiredObj.(*coreapi.Pod); ok {
		return pod
	}

	return nil
}
