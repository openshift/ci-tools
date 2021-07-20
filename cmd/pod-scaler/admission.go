package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pjutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"

	buildv1 "github.com/openshift/api/build/v1"
	buildclientv1 "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	pod_scaler "github.com/openshift/ci-tools/pkg/pod-scaler"
	"github.com/openshift/ci-tools/pkg/rehearse"
	"github.com/openshift/ci-tools/pkg/steps"
)

func admit(port, healthPort int, certDir string, client buildclientv1.BuildV1Interface, cpu, memory []*cacheReloader, mutateResources bool) {
	logger := logrus.WithField("component", "admission")
	logger.Info("Initializing admission webhook server.")
	health := pjutil.NewHealthOnPort(healthPort)
	resources := newResourceServer(cpu, memory, health)
	decoder, err := admission.NewDecoder(scheme.Scheme)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create decoder from scheme.")
	}
	server := webhook.Server{
		Port:    port,
		CertDir: certDir,
	}
	server.Register("/pods", &webhook.Admission{Handler: &podMutator{logger: logger, client: client, decoder: decoder, resources: resources, mutateResources: mutateResources}})
	logger.Info("Serving admission webhooks.")
	if err := server.StartStandalone(interrupts.Context(), nil); err != nil {
		logrus.WithError(err).Fatal("Failed to serve webhooks.")
	}
}

type podMutator struct {
	logger          *logrus.Entry
	client          buildclientv1.BuildV1Interface
	resources       *resourceServer
	mutateResources bool
	decoder         *admission.Decoder
}

func (m *podMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}

	err := m.decoder.Decode(req, pod)
	if err != nil {
		logrus.WithError(err).Error("Failed to decode raw object as Pod.")
		return admission.Errored(http.StatusBadRequest, err)
	}
	logger := m.logger.WithField("name", pod.Name)
	buildName, isBuildPod := pod.Annotations[buildv1.BuildLabel]
	if isBuildPod {
		logger = logger.WithField("build", buildName)
		logger.Trace("Handling labels on Pod created for a Build.")
		build, err := m.client.Builds(pod.Namespace).Get(ctx, buildName, metav1.GetOptions{})
		if err != nil {
			logger.WithError(err).Error("Could not get Build for Pod.")
			return admission.Allowed("Could not get Build for Pod, ignoring.")
		}
		mutatePodLabels(pod, build)
	}
	if err := mutatePodMetadata(pod); err != nil {
		logger.WithError(err).Error("Failed to handle rehearsal Pod.")
		return admission.Allowed("Failed to handle rehearsal Pod, ignoring.")
	}
	if m.mutateResources {
		mutatePodResources(pod, m.resources)
	}

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		logger.WithError(err).Error("Could not marshal mutated Pod.")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	response := admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
	// we need these to be deterministically ordered for testing
	sort.Slice(response.Patches, func(i, j int) bool {
		if response.Patches[i].Operation != response.Patches[j].Operation {
			return response.Patches[i].Operation < response.Patches[j].Operation
		}
		return response.Patches[i].Path < response.Patches[j].Path
	})
	return response
}

// mutatePodMetadata updates metadata labels for Pods created by Prow for rehearsals,
// where default metadata points to the release repo instead of the repo under test.
// We can fix this by updating to use the values from the configuration that the job
// ends up running with.
func mutatePodMetadata(pod *corev1.Pod) error {
	if _, isRehearsal := pod.ObjectMeta.Labels[rehearse.Label]; !isRehearsal {
		return nil
	}
	var rawConfig string
	for _, container := range pod.Spec.Containers {
		if container.Name != "test" {
			continue
		}
		for _, value := range container.Env {
			if value.Name != "CONFIG_SPEC" {
				continue
			}
			rawConfig = value.Value
		}
	}
	if rawConfig == "" {
		return errors.New("could not find configuration in rehearsal Pod's env")
	}
	var config api.ReleaseBuildConfiguration
	if err := yaml.Unmarshal([]byte(rawConfig), &config); err != nil {
		return fmt.Errorf("could not unmarshal configuration from rehearsal pod: %w", err)
	}
	pod.ObjectMeta.Labels[kube.ContextAnnotation] = pod.ObjectMeta.Labels[rehearse.LabelContext]
	pod.ObjectMeta.Labels[kube.OrgLabel] = config.Metadata.Org
	pod.ObjectMeta.Labels[kube.RepoLabel] = config.Metadata.Repo
	pod.ObjectMeta.Labels[kube.BaseRefLabel] = config.Metadata.Branch
	return nil
}

func mutatePodLabels(pod *corev1.Pod, build *buildv1.Build) {
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	for _, label := range []string{steps.LabelMetadataOrg, steps.LabelMetadataRepo, steps.LabelMetadataBranch, steps.LabelMetadataVariant, steps.LabelMetadataTarget} {
		buildValue, buildHas := build.Labels[label]
		_, podHas := pod.Labels[label]
		if buildHas && !podHas {
			pod.Labels[label] = buildValue
		}
	}
}

// useOursIfLarger updates fields in theirs when ours are larger
func useOursIfLarger(allOfOurs, allOfTheirs *corev1.ResourceRequirements) {
	for _, item := range []*corev1.ResourceRequirements{allOfOurs, allOfTheirs} {
		if item.Requests == nil {
			item.Requests = corev1.ResourceList{}
		}
		if item.Limits == nil {
			item.Limits = corev1.ResourceList{}
		}
	}
	for _, pair := range []struct {
		ours, theirs *corev1.ResourceList
	}{
		{ours: &allOfOurs.Requests, theirs: &allOfTheirs.Requests},
		{ours: &allOfOurs.Limits, theirs: &allOfTheirs.Limits},
	} {
		for _, field := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			our := (*pair.ours)[field]
			their := (*pair.theirs)[field]
			if our.Cmp(their) == 1 {
				(*pair.theirs)[field] = our
			}
		}
	}
}

// reconcileLimits ensures that container resource limits do not set anything for CPU (as we
// are fairly certain this is never a useful thing to do) and that the limits are >=200% of
// requests (which they may not be any longer if we've changed requests)
func reconcileLimits(resources *corev1.ResourceRequirements) {
	if resources.Limits == nil {
		return
	}
	delete(resources.Limits, corev1.ResourceCPU)
	// Note: doing math on Quantities is not easy, since they may contain values that overflow
	// normal integers. Doing math on inf.Dec is possible, but there does not exist any way to
	// convert back from an inf.Dec to a resource.Quantity. So, while we would want to have a
	// limit threshold like 120% or similar, we use 200% as that's what is trivially easy to
	// accomplish with the math we can do on resource.Quantity.
	minimumLimit := resources.Requests[corev1.ResourceMemory]
	minimumLimit.Add(minimumLimit)
	currentLimit := resources.Limits[corev1.ResourceMemory]
	if currentLimit.Cmp(minimumLimit) == -1 {
		resources.Limits[corev1.ResourceMemory] = minimumLimit
	}
}

func mutatePodResources(pod *corev1.Pod, server *resourceServer) {
	for i := range pod.Spec.InitContainers {
		meta := pod_scaler.MetadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, pod.Spec.InitContainers[i].Name)
		resources, recommendationExists := server.recommendedRequestFor(meta)
		if recommendationExists {
			useOursIfLarger(&resources, &pod.Spec.InitContainers[i].Resources)
			reconcileLimits(&pod.Spec.InitContainers[i].Resources)
		}
	}
	for i := range pod.Spec.Containers {
		meta := pod_scaler.MetadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, pod.Spec.Containers[i].Name)
		resources, recommendationExists := server.recommendedRequestFor(meta)
		if recommendationExists {
			useOursIfLarger(&resources, &pod.Spec.Containers[i].Resources)
			reconcileLimits(&pod.Spec.Containers[i].Resources)
		}
	}
}
