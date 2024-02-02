package main

import (
	"context"
	"fmt"

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
)

const nvidiaGPU = "nvidia.com/gpu"

var (
	nvidiaGPUToleration = corev1.Toleration{
		Key:      nvidiaGPU,
		Operator: corev1.TolerationOpEqual,
		Value:    "true",
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
	rootCmd.Flags().IntVar(&opts.port, "port", 0, "Port the server will listen on")
}

func setupLogger() logr.Logger {
	innerLogger := logrus.New()
	innerLogger.Formatter = &logrus.JSONFormatter{}
	log.SetLogger(logrusr.New(innerLogger))
	return log.Log
}

type options struct {
	certDir string
	port    int
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

	if !gpuNeeded {
		gpuNeeded = hasNvidaGPURequest(logger, pod.Spec.Containers)
	}

	if gpuNeeded {
		addToleration(logger, pod)
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
	_, requestExists := requirement.Requests[nvidiaGPU]
	_, limitExists := requirement.Limits[nvidiaGPU]
	return requestExists || limitExists
}

func startWebhookServer(ctx context.Context, logger *logr.Logger, o *options, cfg *rest.Config) error {
	logger.Info("Setting up manager")
	mgr, err := manager.New(cfg, manager.Options{
		WebhookServer: webhook.NewServer(webhook.Options{
			CertDir: o.certDir,
			Port:    o.port,
		}),
	})
	if err != nil {
		logger.Error(err, "Unable to set up manager")
		return err
	}

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

	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("get cluster config: %w", err)
	}

	return startWebhookServer(cmd.Context(), &logger, &opts, cfg)
}

func main() {
	cobra.CheckErr(rootCmd.ExecuteContext(signals.SetupSignalHandler()))
}
