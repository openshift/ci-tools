package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"

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

func admit(port, healthPort int, certDir string, client buildclientv1.BuildV1Interface) {
	logger := logrus.WithField("component", "admission")
	logger.Info("Initializing admission webhook server.")
	health := pjutil.NewHealthOnPort(healthPort)
	health.ServeReady()
	decoder, err := admission.NewDecoder(scheme.Scheme)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create decoder from scheme.")
	}
	server := webhook.Server{
		Port:    port,
		CertDir: certDir,
	}
	server.Register("/pods", &webhook.Admission{Handler: &podMutator{logger: logger, client: client, decoder: decoder}})
	logger.Info("Serving admission webhooks.")
	if err := server.StartStandalone(interrupts.Context(), nil); err != nil {
		logrus.WithError(err).Fatal("Failed to serve webhooks.")
	}
}

type podMutator struct {
	logger  *logrus.Entry
	client  buildclientv1.BuildV1Interface
	decoder *admission.Decoder
}

func (m *podMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}

	err := m.decoder.Decode(req, pod)
	if err != nil {
		logrus.WithError(err).Error("Failed to decode raw object as Pod.")
		return admission.Errored(http.StatusBadRequest, err)
	}
	buildName, isBuildPod := pod.Annotations[buildv1.BuildLabel]
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
	for _, label := range []string{steps.LabelMetadataOrg, steps.LabelMetadataRepo, steps.LabelMetadataBranch, steps.LabelMetadataVariant, steps.LabelMetadataTarget, steps.LabelMetadataStep} {
		buildValue, buildHas := build.Labels[label]
		_, podHas := pod.Labels[label]
		if buildHas && !podHas {
			pod.Labels[label] = buildValue
		}
	}
}
