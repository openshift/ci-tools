package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/simplifypath"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	buildv1 "github.com/openshift/api/build/v1"
	buildclientv1 "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"

	"github.com/openshift/ci-tools/pkg/steps"
)

// l keeps the tree legible
func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

var (
	admissionMetrics = metrics.NewMetrics("pod_scaler_admission")
)

func admit(port int, client buildclientv1.BuildV1Interface) {
	logger := logrus.WithField("component", "admission")
	logger.Info("Initializing admission webhook server.")
	health := pjutil.NewHealth()
	health.ServeReady()
	mutator, err := admission.StandaloneWebhook(&webhook.Admission{Handler: &podMutator{logger: logger, client: client}}, admission.StandaloneOptions{
		MetricsPath: "/pods",
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create pod mutator.")
	}
	metrics.ExposeMetrics("pod_scaler_admission", prowConfig.PushGateway{}, flagutil.DefaultMetricsPort)
	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicing the root
		l("pods"),
	))
	handler := metrics.TraceHandler(simplifier, admissionMetrics.HTTPRequestDuration, admissionMetrics.HTTPResponseSize)
	mux := http.NewServeMux()
	mux.Handle("/pods", handler(mutator))
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: mux}
	logger.Info("Serving admission webhooks.")
	interrupts.ListenAndServe(httpServer, 5*time.Second)
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
		return admission.Errored(http.StatusBadRequest, err)
	}
	buildName, isBuildPod := pod.Labels[buildv1.BuildLabel]
	if !isBuildPod {
		return admission.Allowed("Not a Pod implementing a Build.")
	}
	logger := m.logger.WithField("build", buildName)
	logger.Debug("Handling labels on Pod created for a Build.")
	build, err := m.client.Builds(pod.Namespace).Get(ctx, buildName, metav1.GetOptions{})
	if err != nil {
		logger.WithError(err).Error("Could not get Build for Pod.")
		return admission.Allowed("Could not get Build for Pod, ignoring.")
	}
	mutatePod(pod, build)

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
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

//nolint:unparam
func (m *podMutator) InjectDecoder(d *admission.Decoder) error {
	m.decoder = d
	return nil
}
