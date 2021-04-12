package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/kube"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	buildv1 "github.com/openshift/api/build/v1"
	buildclientv1 "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"

	"github.com/openshift/ci-tools/pkg/steps"
)

func admit(port, healthPort int, certDir string, client buildclientv1.BuildV1Interface, cpu, memory []*cacheReloader) {
	logger := logrus.WithField("component", "admission")
	logger.Info("Initializing admission webhook server.")
	health := pjutil.NewHealthOnPort(healthPort)
	resources := newResourceServer(cpu, memory, health)
	health.ServeReady()
	decoder, err := admission.NewDecoder(scheme.Scheme)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create decoder from scheme.")
	}
	server := webhook.Server{
		Port:    port,
		CertDir: certDir,
	}
	server.Register("/pods", &webhook.Admission{Handler: &podMutator{logger: logger, client: client, decoder: decoder, resources: resources}})
	logger.Info("Serving admission webhooks.")
	if err := server.StartStandalone(interrupts.Context(), nil); err != nil {
		logrus.WithError(err).Fatal("Failed to serve webhooks.")
	}
}

type podMutator struct {
	logger    *logrus.Entry
	client    buildclientv1.BuildV1Interface
	resources *resourceServer
	decoder   *admission.Decoder
}

func (m *podMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}

	err := m.decoder.Decode(req, pod)
	if err != nil {
		logrus.WithError(err).Error("Failed to decode raw object as Pod.")
		return admission.Errored(http.StatusBadRequest, err)
	}
	buildName, isBuildPod := pod.Labels[buildv1.BuildLabel]
	if !isBuildPod {
		logrus.Trace("Allowing Pod, it is not implementing a Build.")
		return admission.Allowed("Not a Pod implementing a Build.")
	}
	logger := m.logger.WithField("build", buildName)
	logger.Trace("Handling labels on Pod created for a Build.")
	build, err := m.client.Builds(pod.Namespace).Get(ctx, buildName, metav1.GetOptions{})
	if err != nil {
		logger.WithError(err).Error("Could not get Build for Pod.")
		return admission.Allowed("Could not get Build for Pod, ignoring.")
	}
	mutatePod(pod, build)

	for i := range pod.Spec.InitContainers {
		meta := metadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, pod.Spec.InitContainers[i].Name)
		resources, recommendationExists := m.resources.recommendedRequestFor(meta)
		if recommendationExists {
			pod.Spec.InitContainers[i].Resources = resources
		}
	}
	for i := range pod.Spec.Containers {
		meta := metadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, pod.Spec.Containers[i].Name)
		resources, recommendationExists := m.resources.recommendedRequestFor(meta)
		if recommendationExists {
			pod.Spec.Containers[i].Resources = resources
		}
	}

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		logger.WithError(err).Error("Could not marshal mutated Pod.")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func mutatePod(pod *corev1.Pod, build *buildv1.Build) {
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

func metadataFor(labels map[string]string, pod, container string) FullMetadata {
	metric := labelsToMetric(labels)
	metric[LabelNamePod] = model.LabelValue(pod)
	metric[LabelNameContainer] = model.LabelValue(container)
	return metadataFromMetric(metric)
}

func labelsToMetric(labels map[string]string) model.Metric {
	mapping := map[string]model.LabelName{
		kube.CreatedByProw:         ProwLabelNameCreated,
		kube.ContextAnnotation:     ProwLabelNameContext,
		kube.ProwJobAnnotation:     ProwLabelNameJob,
		kube.ProwJobTypeLabel:      ProwLabelNameType,
		kube.OrgLabel:              ProwLabelNameOrg,
		kube.RepoLabel:             ProwLabelNameRepo,
		kube.BaseRefLabel:          ProwLabelNameBranch,
		steps.LabelMetadataOrg:     LabelNameOrg,
		steps.LabelMetadataRepo:    LabelNameRepo,
		steps.LabelMetadataBranch:  LabelNameBranch,
		steps.LabelMetadataVariant: LabelNameVariant,
		steps.LabelMetadataTarget:  LabelNameTarget,
		steps.LabelMetadataStep:    LabelNameStep,
		buildv1.BuildLabel:         LabelNameBuild,
		release.Label:              LabelNameRelease,
		steps.AppLabel:             LabelNameApp,
	}
	output := model.Metric{}
	for key, value := range labels {
		mapped, recorded := mapping[key]
		if recorded {
			output[mapped] = model.LabelValue(value)
		}
	}
	return output
}
