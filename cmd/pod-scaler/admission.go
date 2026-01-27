package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/prow/pkg/entrypoint"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/yaml"

	buildv1 "github.com/openshift/api/build/v1"
	buildclientv1 "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
	"github.com/openshift/ci-tools/pkg/rehearse"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
)

func admit(port, healthPort int, certDir string, client buildclientv1.BuildV1Interface, loaders map[string][]*cacheReloader, mutateResourceLimits bool, cpuCap int64, memoryCap string, cpuPriorityScheduling int64, authoritativeCPU, authoritativeMemory bool, reporter results.PodScalerReporter) {
	logger := logrus.WithField("component", "pod-scaler admission")
	logger.Infof("Initializing admission webhook server with %d loaders.", len(loaders))
	health := pjutil.NewHealthOnPort(healthPort)
	resources := newResourceServer(loaders, health)
	decoder := admission.NewDecoder(scheme.Scheme)

	server := webhook.NewServer(webhook.Options{
		Port:    port,
		CertDir: certDir,
	})
	server.Register("/pods", &webhook.Admission{Handler: &podMutator{logger: logger, client: client, decoder: decoder, resources: resources, mutateResourceLimits: mutateResourceLimits, cpuCap: cpuCap, memoryCap: memoryCap, cpuPriorityScheduling: cpuPriorityScheduling, authoritativeCPU: authoritativeCPU, authoritativeMemory: authoritativeMemory, reporter: reporter}})
	logger.Info("Serving admission webhooks.")
	if err := server.Start(interrupts.Context()); err != nil {
		logrus.WithError(err).Fatal("Failed to serve webhooks.")
	}
}

type podMutator struct {
	logger                *logrus.Entry
	client                buildclientv1.BuildV1Interface
	resources             *resourceServer
	mutateResourceLimits  bool
	decoder               admission.Decoder
	cpuCap                int64
	memoryCap             string
	cpuPriorityScheduling int64
	authoritativeCPU      bool
	authoritativeMemory   bool
	reporter              results.PodScalerReporter
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

	scalePod, err := shouldScalePod(pod)
	if err != nil {
		logger.WithError(err).Error("Could not determine if pod should be scaled")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if !scalePod {
		return admission.Allowed("ignoring pod due to presence of annotation")
	}

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
	if err := mutatePodMetadata(pod, logger); err != nil {
		logger.WithError(err).Error("Failed to handle rehearsal Pod.")
		return admission.Allowed("Failed to handle rehearsal Pod, ignoring.")
	}
	mutatePodResources(pod, m.resources, m.mutateResourceLimits, m.cpuCap, m.memoryCap, m.authoritativeCPU, m.authoritativeMemory, m.reporter, logger)
	m.addPriorityClass(pod)

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

func shouldScalePod(pod *corev1.Pod) (bool, error) {
	scale, present := pod.Annotations["ci-workload-autoscaler.openshift.io/scale"]
	if present {
		return strconv.ParseBool(scale)
	}
	// By default, the pod should be scaled if the annotation is not present
	return true, nil
}

// mutatePodMetadata updates metadata labels for Pods created by Prow for rehearsals,
// where default metadata points to the release repo instead of the repo under test.
// We can fix this by updating to use the values from the configuration that the job
// ends up running with.
func mutatePodMetadata(pod *corev1.Pod, logger *logrus.Entry) error {
	if _, isRehearsal := pod.ObjectMeta.Labels[rehearse.Label]; !isRehearsal {
		return nil
	}
	var rawConfig, rawEntrypointConfig string
	var foundContainer bool
	for _, container := range pod.Spec.Containers {
		if container.Name != "test" {
			continue
		}
		foundContainer = true
		for _, value := range container.Env {
			if value.Name == "CONFIG_SPEC" {
				rawConfig = value.Value
			}
			if value.Name == entrypoint.JSONConfigEnvVar {
				rawEntrypointConfig = value.Value
			}
		}
	}
	if rawConfig == "" {
		if foundContainer {
			baseError := "could not find $CONFIG_SPEC in the environment of the rehearsal Pod's test container"
			if rawEntrypointConfig != "" {
				var opts entrypoint.Options
				if err := json.Unmarshal([]byte(rawEntrypointConfig), &opts); err != nil {
					return fmt.Errorf("%s, could not parse $ENTRYPOINT_OPTIONS: %w", baseError, err)
				}
				if len(opts.Args) > 0 && opts.Args[0] != "ci-operator" {
					logger.Debugf("ignoring Pod, %s, $ENTRYPOINT_OPTIONS is running %s, not ci-operator", baseError, opts.Args[0])
					return nil
				}
			}
			return errors.New(baseError)
		}
		return errors.New("could not find test container in the rehearsal Pod")
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
	backfilledFromBuild := false
	for _, label := range []string{steps.LabelMetadataOrg, steps.LabelMetadataRepo, steps.LabelMetadataBranch, steps.LabelMetadataVariant, steps.LabelMetadataTarget} {
		buildValue, buildHas := build.Labels[label]
		_, podHas := pod.Labels[label]
		if buildHas && !podHas {
			pod.Labels[label] = buildValue
			backfilledFromBuild = true
		}
	}
	if backfilledFromBuild {
		pod.Labels[steps.CreatedByCILabel] = "true"
	}
}

// applyRecommendationsBasedOnRecentData applies resource recommendations based on recent usage data
// (see resourceRecommendationWindow). If they used more, we increase resources. If they used less,
// we decrease them if authoritative mode is enabled for that resource type.
//
// TestApplyRecommendationsBasedOnRecentData_ReducesResources is tested in admission_test.go
// as part of TestUseOursIfLarger. The reduction functionality is verified there with proper
// test cases that handle ResourceQuantity comparison correctly.
func applyRecommendationsBasedOnRecentData(allOfOurs, allOfTheirs *corev1.ResourceRequirements, workloadName, workloadType string, authoritativeCPU, authoritativeMemory bool, reporter results.PodScalerReporter, logger *logrus.Entry) {
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
		resource     string
	}{
		{ours: &allOfOurs.Requests, theirs: &allOfTheirs.Requests, resource: "request"},
		{ours: &allOfOurs.Limits, theirs: &allOfTheirs.Limits, resource: "limit"},
	} {
		for _, field := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			our := (*pair.ours)[field]
			// If we have no recommendation for this resource, skip it
			if our.IsZero() {
				continue
			}
			//TODO(sgoeddel): this is a temporary experiment to see what effect setting values that are 120% of what has
			// been determined has on the rate of OOMKilled and similar termination of workloads
			increased := our.AsApproximateFloat64() * 1.2
			our.Set(int64(increased))

			their := (*pair.theirs)[field]
			fieldLogger := logger.WithFields(logrus.Fields{
				"workloadName": workloadName,
				"workloadType": workloadType,
				"field":        field,
				"resource":     pair.resource,
				"determined":   our.String(),
				"configured":   their.String(),
			})
			cmp := our.Cmp(their)
			if cmp == 1 {
				fieldLogger.Debug("determined amount larger than configured, increasing resources")
				(*pair.theirs)[field] = our
				if their.Value() > 0 && our.Value() > (their.Value()*10) {
					reporter.ReportResourceConfigurationWarning(workloadName, workloadType, their.String(), our.String(), field.String())
				}
			} else if cmp < 0 {
				// Check if authoritative mode is enabled for this resource type
				isAuthoritative := false
				if field == corev1.ResourceCPU {
					isAuthoritative = authoritativeCPU
				} else if field == corev1.ResourceMemory {
					isAuthoritative = authoritativeMemory
				}

				if !isAuthoritative {
					fieldLogger.Debug("authoritative mode disabled for this resource, skipping reduction")
					continue
				}

				// Apply gradual reduction with safety limits: max 25% reduction per cycle, minimum 5% difference
				ourValue := our.AsApproximateFloat64()
				theirValue := their.AsApproximateFloat64()
				if theirValue == 0 {
					fieldLogger.Debug("theirs is zero, applying recommendation")
					(*pair.theirs)[field] = our
					continue
				}

				reductionPercent := 1.0 - (ourValue / theirValue)
				if reductionPercent < 0.05 {
					fieldLogger.Debug("difference less than 5%, skipping micro-adjustment")
					continue
				}

				maxReductionPercent := 0.25
				if reductionPercent > maxReductionPercent {
					maxAllowed := theirValue * (1.0 - maxReductionPercent)
					our.Set(int64(maxAllowed))
					fieldLogger.Debugf("applying gradual reduction (limited to 25%% per cycle)")
				} else {
					fieldLogger.Debug("reducing resources based on recent usage")
				}
				(*pair.theirs)[field] = our
			} else {
				fieldLogger.Debug("determined amount equal to configured")
			}
		}
	}
}

// reconcileLimits ensures that container resource limits do not set anything for CPU (as we
// are fairly certain this is never a useful thing to do) and that any limits that have been configured
// are >=200% of requests (which they may not be any longer if we've changed requests)
func reconcileLimits(resources *corev1.ResourceRequirements) {
	if resources.Limits == nil {
		return
	}
	delete(resources.Limits, corev1.ResourceCPU)
	if !resources.Limits.Memory().IsZero() { // Never set a limit where there isn't one defined
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
}

func preventUnschedulable(resources *corev1.ResourceRequirements, cpuCap int64, memoryCap string, logger *logrus.Entry) {
	if resources.Requests == nil {
		logger.Debug("no requests, skipping")
		return
	}

	if _, ok := resources.Requests[corev1.ResourceCPU]; ok {
		// TODO(DPTP-2525): Make cluster-specific?
		cpuRequestCap := *resource.NewQuantity(cpuCap, resource.DecimalSI)
		if resources.Requests.Cpu().Cmp(cpuRequestCap) == 1 {
			logger.Debugf("setting original CPU request of: %s to cap", resources.Requests.Cpu())
			resources.Requests[corev1.ResourceCPU] = cpuRequestCap
		}
	}

	if _, ok := resources.Requests[corev1.ResourceMemory]; ok {
		memoryRequestCap := resource.MustParse(memoryCap)
		if resources.Requests.Memory().Cmp(memoryRequestCap) == 1 {
			logger.Debugf("setting original memory request of: %s to cap", resources.Requests.Memory())
			resources.Requests[corev1.ResourceMemory] = memoryRequestCap
		}
	}
}

func mutatePodResources(pod *corev1.Pod, server *resourceServer, mutateResourceLimits bool, cpuCap int64, memoryCap string, authoritativeCPU, authoritativeMemory bool, reporter results.PodScalerReporter, logger *logrus.Entry) {
	mutateResources := func(containers []corev1.Container) {
		for i := range containers {
			meta := podscaler.MetadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, containers[i].Name)
			resources, recommendationExists := server.recommendedRequestFor(meta)
			if recommendationExists {
				logger.Debugf("recommendation exists for: %s", containers[i].Name)
				workloadType := determineWorkloadType(pod.Annotations, pod.Labels)
				workloadName := determineWorkloadName(pod.Name, containers[i].Name, workloadType, pod.Labels)
				applyRecommendationsBasedOnRecentData(&resources, &containers[i].Resources, workloadName, workloadType, authoritativeCPU, authoritativeMemory, reporter, logger)
				if mutateResourceLimits {
					reconcileLimits(&containers[i].Resources)
				}
			}
			preventUnschedulable(&containers[i].Resources, cpuCap, memoryCap, logger)
		}
	}
	mutateResources(pod.Spec.InitContainers)
	mutateResources(pod.Spec.Containers)
}

const (
	WorkloadTypeProwjob   = "prowjob"
	WorkloadTypeBuild     = "build"
	WorkloadTypeStep      = "step"
	WorkloadTypeUndefined = "undefined"
)

// determineWorkloadType returns the workload type to be used in metrics
func determineWorkloadType(annotations, labels map[string]string) string {
	if _, isBuildPod := annotations[buildv1.BuildLabel]; isBuildPod {
		return WorkloadTypeBuild
	}
	if _, isProwjob := labels["prow.k8s.io/job"]; isProwjob {
		return WorkloadTypeProwjob
	}
	if _, isStep := labels[steps.LabelMetadataStep]; isStep {
		return WorkloadTypeStep
	}
	return WorkloadTypeUndefined
}

// determineWorkloadName returns the workload name we will see in the metrics
func determineWorkloadName(podName, containerName, workloadType string, labels map[string]string) string {
	if workloadType == WorkloadTypeProwjob {
		return labels["prow.k8s.io/job"]
	}
	return fmt.Sprintf("%s-%s", podName, containerName)
}

const priorityClassName = "high-priority-nonpreempting"

func (m *podMutator) addPriorityClass(pod *corev1.Pod) {
	shouldAdd := func(containers []corev1.Container) bool {
		for _, container := range containers {
			quantityForPriorityScheduling := *resource.NewQuantity(m.cpuPriorityScheduling, resource.DecimalSI)
			if container.Resources.Requests.Cpu().Cmp(quantityForPriorityScheduling) >= 0 {
				return true
			}
		}
		return false
	}

	if shouldAdd(pod.Spec.Containers) || shouldAdd(pod.Spec.InitContainers) {
		pod.Spec.Priority = nil         // We cannot have Priority defined if we add the PriorityClassName
		pod.Spec.PreemptionPolicy = nil // We cannot have PreemptionPolicy defined if we are using a priority class with preemption of "Never"
		pod.Spec.PriorityClassName = priorityClassName
	}
}
