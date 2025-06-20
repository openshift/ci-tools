package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/bombsimon/logrusr/v3"
	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/openshift/ci-tools/pkg/api"
)

var (
	nvidiaGPUToleration = corev1.Toleration{
		Key:      api.NvidiaGPUResource,
		Operator: corev1.TolerationOpEqual,
		Value:    "true",
		Effect:   corev1.TaintEffectNoSchedule,
	}

	KVMVirtToleration = corev1.Toleration{
		Key:      "ci-workload",
		Operator: corev1.TolerationOpEqual,
		Value:    "virt-launcher",
		Effect:   corev1.TaintEffectNoSchedule,
	}

	opts = options{}

	rootCmd = &cobra.Command{
		Use:   "gpu-scheduling-webhook",
		Short: "Controls where pods will be scheduled when they request a GPU",
		Long: `Controls where pods will be scheduled when they request a GPU.

Example:
$ gpu-scheduling-webhook --cert-dir=<cert-dir> --port=443`,
		RunE: RunE,
	}
)

func init() {
	rootCmd.Flags().StringVar(&opts.certDir, "cert-dir", "", "A folder holding the server private key and and certicate for TLS")
	rootCmd.Flags().StringVar(&opts.healthProbeAddr, "health-probe-addr", ":8081", "Health probe binding address <addr>:<port>. Default to :8081")
	rootCmd.Flags().IntVar(&opts.port, "port", 0, "Port the server will listen on")
}

func setupLogger() logr.Logger {
	innerLogger := logrus.New()
	innerLogger.Formatter = &logrus.JSONFormatter{}
	log.SetLogger(logrusr.New(innerLogger))
	return log.Log
}

type options struct {
	certDir         string
	port            int
	healthProbeAddr string
}

type gpuTolerator struct{}

func (*gpuTolerator) Default(ctx context.Context, obj runtime.Object) error {
	logger := log.FromContext(ctx)

	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("expected a Pod but got a %T", obj)
	}

	logger = logger.WithValues("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))

	// Check if a GPU required on resources from containers and init containers.
	var gpuNeeded bool
	gpuNeeded = hasNvidaGPURequest(logger, pod.Spec.InitContainers)

	var KVMVirtNeeded bool
	KVMVirtNeeded = hasKVMVirtRequest(logger, pod.Spec.InitContainers)

	if !gpuNeeded {
		gpuNeeded = hasNvidaGPURequest(logger, pod.Spec.Containers)
	}

	if gpuNeeded {
		addToleration(logger, pod)
	}

	if !KVMVirtNeeded {
		KVMVirtNeeded = hasKVMVirtRequest(logger, pod.Spec.Containers)
	}

	if KVMVirtNeeded {
		addKVMVirtToleration(logger, pod)
	}

	return nil
}

func hasNvidaGPURequest(logger logr.Logger, containers []corev1.Container) bool {
	for i := range containers {
		c := &containers[i]
		if needNvidiaGPU(c.Resources) {
			logger.Info("Request Nvidia GPU", "container", c.Name)
			return true
		}
	}
	return false
}

func hasKVMVirtRequest(logger logr.Logger, containers []corev1.Container) bool {
	for i := range containers {
		c := &containers[i]
		if needKVMVirt(c.Resources) {
			logger.Info("Request KVM Virt", "container", c.Name)
			return true
		}
	}
	return false
}

func needKVMVirt(requirement corev1.ResourceRequirements) bool {
	_, requestExists := requirement.Requests["devices.kubevirt.io/kvm"]
	_, limitExists := requirement.Limits["devices.kubevirt.io/kvm"]
	return requestExists || limitExists
}

// Allow a pod to be scheduled on the KVM Virt featured node by adding a toleration.
// Do nothing if the toleration has already been added.
func addKVMVirtToleration(logger logr.Logger, pod *corev1.Pod) {
	var tolerationExists bool
	for _, t := range pod.Spec.Tolerations {
		if t == KVMVirtToleration {
			tolerationExists = true
			break
		}
	}

	if !tolerationExists {
		pod.Spec.Tolerations = append(pod.Spec.Tolerations, KVMVirtToleration)
		logger.Info("Add KVM Virt toleration")
	} else {
		logger.Info("KVM Virt toleration exists already")
	}
}

// Allow a pod to be scheduled on the GPU featured node by adding a toleration.
// Do nothing if the toleration has already been added.
func addToleration(logger logr.Logger, pod *corev1.Pod) {
	var tolerationExists bool
	for _, t := range pod.Spec.Tolerations {
		if t == nvidiaGPUToleration {
			tolerationExists = true
			break
		}
	}

	if !tolerationExists {
		pod.Spec.Tolerations = append(pod.Spec.Tolerations, nvidiaGPUToleration)
		logger.Info("Add toleration")
	} else {
		logger.Info("Toleration exists already")
	}
}

func needNvidiaGPU(requirement corev1.ResourceRequirements) bool {
	_, requestExists := requirement.Requests[api.NvidiaGPUResource]
	_, limitExists := requirement.Limits[api.NvidiaGPUResource]
	return requestExists || limitExists
}

func startWebhookServer(ctx context.Context, logger *logr.Logger, o *options, cfg *rest.Config) error {
	logger.Info("Setting up manager")
	mgr, err := manager.New(cfg, manager.Options{
		HealthProbeBindAddress: o.healthProbeAddr,
		WebhookServer: webhook.NewServer(webhook.Options{
			CertDir: o.certDir,
			Port:    o.port,
		}),
	})
	if err != nil {
		logger.Error(err, "Unable to set up manager")
		return err
	}

	if err := mgr.AddHealthzCheck("healthz", func(req *http.Request) error { return nil }); err != nil {
		logger.Error(err, "Unable to set up healthz endpoint")
		return err
	}

	if err := mgr.AddReadyzCheck("readyz", func(req *http.Request) error { return nil }); err != nil {
		logger.Error(err, "Unable to set up readyz endpoint")
		return err
	}

	logger.WithValues("Addr", o.healthProbeAddr).Info("Serving healthiness probes")

	if err := builder.WebhookManagedBy(mgr).
		For(&corev1.Pod{}).
		WithDefaulter(&gpuTolerator{}).
		Complete(); err != nil {
		logger.Error(err, "Unable to build webhook")
		return err
	}

	logger.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		logger.Error(err, "Unable to start manager")
		return err
	}

	return nil
}

func RunE(cmd *cobra.Command, args []string) error {
	logger := setupLogger().WithName("gpu-scheduling")
	logger.Info("Starting the webhook")

	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("get cluster config: %w", err)
	}

	return startWebhookServer(cmd.Context(), &logger, &opts, cfg)
}

func main() {
	cobra.CheckErr(rootCmd.ExecuteContext(signals.SetupSignalHandler()))
}
