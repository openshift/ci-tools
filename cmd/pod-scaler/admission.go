package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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

type authoritativePair struct {
	apply        bool
	dryRun       bool
	maxReduction float64
}

type authoritativeConfig struct {
	cpuRequest, cpuLimit, memoryRequest, memoryLimit authoritativePair
}

func (c authoritativeConfig) pair(field corev1.ResourceName, resourceType string) authoritativePair {
	switch {
	case field == corev1.ResourceCPU && resourceType == "request":
		return c.cpuRequest
	case field == corev1.ResourceCPU:
		return c.cpuLimit
	case resourceType == "request":
		return c.memoryRequest
	}
	return c.memoryLimit
}

func (c authoritativeConfig) anyDryRun() bool {
	return c.cpuRequest.dryRun || c.cpuLimit.dryRun || c.memoryRequest.dryRun || c.memoryLimit.dryRun
}

type authoritativeDecreaseUsageBasis string

const (
	authoritativeDecreaseUsageP80  authoritativeDecreaseUsageBasis = "p80"
	authoritativeDecreaseUsagePeak authoritativeDecreaseUsageBasis = "peak"
)

func parseAuthoritativeDecreaseUsageBasis(raw string) (authoritativeDecreaseUsageBasis, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "p80":
		return authoritativeDecreaseUsageP80, nil
	case "peak", "max":
		return authoritativeDecreaseUsagePeak, nil
	default:
		return "", fmt.Errorf("unknown authoritative decrease usage basis %q", raw)
	}
}

func usageForAuthoritativeDecrease(recommended corev1.ResourceRequirements, field corev1.ResourceName, basis authoritativeDecreaseUsageBasis) (resource.Quantity, bool) {
	if basis == authoritativeDecreaseUsagePeak {
		if peak, ok := recommended.Limits[field]; ok && !peak.IsZero() {
			return peak, true
		}
	}
	request, ok := recommended.Requests[field]
	if !ok || request.IsZero() {
		return resource.Quantity{}, false
	}
	return request, true
}

func admit(port, healthPort int, certDir string, client buildclientv1.BuildV1Interface, kubeClient kubernetes.Interface, loaders map[string][]*cacheReloader, mutateResourceLimits bool, cpuCap int64, memoryCap string, cpuPriorityScheduling int64, percentageMeasured float64, measuredPodCPUIncrease float64, systemReservedCPU int64, authoritative authoritativeConfig, authoritativeDecreaseUsage authoritativeDecreaseUsageBasis, escalations *escalationServer, reporter results.PodScalerReporter) {
	logger := logrus.WithField("component", "pod-scaler admission")
	logger.Infof("Initializing admission webhook server with %d loaders.", len(loaders))
	if authoritative.anyDryRun() {
		logger.WithFields(logrus.Fields{
			"authoritative_cpu_request_dry_run":    authoritative.cpuRequest.dryRun,
			"authoritative_cpu_limit_dry_run":      authoritative.cpuLimit.dryRun,
			"authoritative_memory_request_dry_run": authoritative.memoryRequest.dryRun,
			"authoritative_memory_limit_dry_run":   authoritative.memoryLimit.dryRun,
		}).Info("authoritative decrease dry-run enabled")
	}
	health := pjutil.NewHealthOnPort(healthPort)
	resources := newResourceServer(loaders, health, cpuCap, memoryCap)
	decoder := admission.NewDecoder(scheme.Scheme)

	// Initialize node allocatable CPU cache
	nodeCache := newNodeAllocatableCache(kubeClient, systemReservedCPU)

	server := webhook.NewServer(webhook.Options{
		Port:    port,
		CertDir: certDir,
	})
	server.Register("/pods", &webhook.Admission{Handler: &podMutator{logger: logger, client: client, decoder: decoder, resources: resources, mutateResourceLimits: mutateResourceLimits, cpuCap: cpuCap, memoryCap: memoryCap, cpuPriorityScheduling: cpuPriorityScheduling, percentageMeasured: percentageMeasured, measuredPodCPUIncrease: measuredPodCPUIncrease, nodeCache: nodeCache, authoritative: authoritative, authoritativeDecreaseUsage: authoritativeDecreaseUsage, escalations: escalations, reporter: reporter}})
	logger.Info("Serving admission webhooks.")
	if err := server.Start(interrupts.Context()); err != nil {
		logrus.WithError(err).Fatal("Failed to serve webhooks.")
	}
}

type podMutator struct {
	logger                     *logrus.Entry
	client                     buildclientv1.BuildV1Interface
	resources                  *resourceServer
	mutateResourceLimits       bool
	decoder                    admission.Decoder
	cpuCap                     int64
	memoryCap                  string
	cpuPriorityScheduling      int64
	percentageMeasured         float64
	measuredPodCPUIncrease     float64
	nodeCache                  *nodeAllocatableCache
	authoritative              authoritativeConfig
	authoritativeDecreaseUsage authoritativeDecreaseUsageBasis
	escalations                *escalationServer
	reporter                   results.PodScalerReporter
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

	// Determine if pod should be measured (check if label already set for idempotency)
	isMeasured := m.shouldBeMeasured(pod)
	if isMeasured {
		m.markPodAsMeasured(pod, logger)
		m.addMeasuredPodAntiaffinity(pod, logger)
	} else if m.percentageMeasured > 0 {
		// Only set the label to "false" if measured pods feature is enabled (for idempotency)
		// This ensures backward compatibility when percentageMeasured is 0
		m.setMeasuredLabel(pod, false, logger)
	}

	mutatePodResources(pod, m.resources, m.mutateResourceLimits, m.cpuCap, m.memoryCap, isMeasured, m.nodeCache, m.measuredPodCPUIncrease, m.authoritative, m.authoritativeDecreaseUsage, m.escalations, m.reporter, logger)
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
	scale, present := pod.Annotations[api.WorkloadAutoscalerScaleAnnotation]
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

var authoritativeMinCPURequest = resource.MustParse("10m")

// useOursIfLarger updates fields in theirs when ours are larger.
func useOursIfLarger(allOfOurs, allOfTheirs *corev1.ResourceRequirements, workloadName, workloadType string, isMeasured bool, workloadClass string, reporter results.PodScalerReporter, logger *logrus.Entry) {
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
			if our.IsZero() {
				continue
			}
			//TODO(sgoeddel): this is a temporary experiment to see what effect setting values that are 120% of what has
			// been determined has on the rate of OOMKilled and similar termination of workloads
			if field == corev1.ResourceCPU {
				our.SetMilli(int64(float64(our.MilliValue()) * 1.2))
			} else {
				increased := our.AsApproximateFloat64() * 1.2
				our.Set(int64(increased))
			}

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
				fieldLogger.Debug("determined amount larger than configured")
				(*pair.theirs)[field] = our
				if their.Value() > 0 && our.Value() > (their.Value()*10) {
					reporter.ReportResourceConfigurationWarning(workloadName, workloadType, their.String(), our.String(), field.String(), isMeasured, workloadClass)
				}
			} else if cmp < 0 {
				fieldLogger.Debug("determined amount smaller than configured")
				continue
			} else {
				fieldLogger.Debug("determined amount equal to configured")
			}
		}
	}
}

// reconcileLimits ensures memory limits are >=200% of requests after request mutation.
func reconcileLimits(resources *corev1.ResourceRequirements) {
	if resources.Limits == nil {
		return
	}
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

func capDigestRequests(resources *corev1.ResourceRequirements, cpuCap, memoryCap resource.Quantity, logger *logrus.Entry) {
	preventUnschedulableWithCaps(resources, cpuCap, memoryCap, logger.WithField("stage", "digest"))
}

func preventUnschedulable(resources *corev1.ResourceRequirements, cpuCap int64, memoryCap string, logger *logrus.Entry) {
	preventUnschedulableWithCaps(resources, *resource.NewQuantity(cpuCap, resource.DecimalSI), resource.MustParse(memoryCap), logger)
}

func preventUnschedulableWithCaps(resources *corev1.ResourceRequirements, cpuCap, memoryCap resource.Quantity, logger *logrus.Entry) {
	if resources.Requests == nil {
		logger.Debug("no requests, skipping")
		return
	}

	if _, ok := resources.Requests[corev1.ResourceCPU]; ok {
		// TODO(DPTP-2525): Make cluster-specific?
		if resources.Requests.Cpu().Cmp(cpuCap) == 1 {
			logger.Debugf("setting original CPU request of: %s to cap", resources.Requests.Cpu())
			resources.Requests[corev1.ResourceCPU] = cpuCap
		}
	}

	if _, ok := resources.Requests[corev1.ResourceMemory]; ok {
		if resources.Requests.Memory().Cmp(memoryCap) == 1 {
			logger.Debugf("setting original memory request of: %s to cap", resources.Requests.Memory())
			resources.Requests[corev1.ResourceMemory] = memoryCap
		}
	}
}

func mutatePodResources(pod *corev1.Pod, server *resourceServer, mutateResourceLimits bool, cpuCap int64, memoryCap string, isMeasured bool, nodeCache *nodeAllocatableCache, measuredPodCPUIncrease float64, authoritative authoritativeConfig, authoritativeDecreaseUsage authoritativeDecreaseUsageBasis, escalations *escalationServer, reporter results.PodScalerReporter, logger *logrus.Entry) {
	workloadClass := pod.Labels[ciWorkloadLabel]

	mutateResources := func(containers []corev1.Container) {
		for i := range containers {
			meta := podscaler.MetadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, containers[i].Name)

			// Get recommendations from both measured and unmeasured runs, use maximum
			var resources corev1.ResourceRequirements
			recommendationExists := false

			// Query for measured recommendation
			meta.Measured = true
			measuredResources, measuredExists := server.recommendedRequestFor(meta)

			// Query for unmeasured recommendation
			meta.Measured = false
			unmeasuredResources, unmeasuredExists := server.recommendedRequestFor(meta)

			// Use maximum from both measured and unmeasured runs
			if measuredExists || unmeasuredExists {
				recommendationExists = true
				resources = corev1.ResourceRequirements{
					Requests: corev1.ResourceList{},
					Limits:   corev1.ResourceList{},
				}

				// Take maximum CPU and memory from both measured and unmeasured runs.
				for _, resourceName := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
					var maxRequest *resource.Quantity
					if measuredExists && measuredResources.Requests != nil {
						if q, ok := measuredResources.Requests[resourceName]; ok {
							maxRequest = &q
						}
					}
					if unmeasuredExists && unmeasuredResources.Requests != nil {
						if q, ok := unmeasuredResources.Requests[resourceName]; ok {
							if maxRequest == nil || q.Cmp(*maxRequest) > 0 {
								maxRequest = &q
							}
						}
					}
					if maxRequest != nil {
						resources.Requests[resourceName] = *maxRequest
					}

					var maxLimit *resource.Quantity
					if measuredExists && measuredResources.Limits != nil {
						if q, ok := measuredResources.Limits[resourceName]; ok {
							maxLimit = &q
						}
					}
					if unmeasuredExists && unmeasuredResources.Limits != nil {
						if q, ok := unmeasuredResources.Limits[resourceName]; ok {
							if maxLimit == nil || q.Cmp(*maxLimit) > 0 {
								maxLimit = &q
							}
						}
					}
					if maxLimit != nil {
						resources.Limits[resourceName] = *maxLimit
					}
				}
			}

			if recommendationExists {
				logger.Debugf("recommendation exists for: %s (using max of measured and unmeasured)", containers[i].Name)
				workloadType := determineWorkloadType(pod.Annotations, pod.Labels)
				workloadName := determineWorkloadName(pod.Name, containers[i].Name, workloadType, pod.Labels)
				useOursIfLarger(&resources, &containers[i].Resources, workloadName, workloadType, isMeasured, workloadClass, reporter, logger)
				if mutateResourceLimits {
					reconcileLimits(&containers[i].Resources)
				}
				applyFailureEscalation(&containers[i].Resources, workloadType, workloadName, escalations, logger)
				applyAuthoritativeLimitDecrease(&resources, &containers[i].Resources, workloadName, workloadType, isMeasured, workloadClass, authoritative, authoritativeDecreaseUsage, escalations, logger)
				if mutateResourceLimits && !authoritative.cpuLimit.apply {
					if containers[i].Resources.Limits != nil {
						delete(containers[i].Resources.Limits, corev1.ResourceCPU)
					}
				}
				clampRequestsToLimits(&containers[i].Resources)
			}
			preventUnschedulable(&containers[i].Resources, cpuCap, memoryCap, logger)
		}
	}
	mutateResources(pod.Spec.InitContainers)
	mutateResources(pod.Spec.Containers)

	// For measured pods, increase CPU requests at pod level (sum of all containers) with proper capping
	if isMeasured {
		increaseCPUForMeasuredPod(pod, nodeCache, measuredPodCPUIncrease, logger)
	}
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

const (
	measuredPodLabel = podscaler.PodLabelMeasured
	ciWorkloadLabel  = "ci-workload"
)

// shouldBeMeasured determines if a pod should be marked as measured based on the configured percentage
// Uses true randomness so all pods have a chance to be measured
func (m *podMutator) shouldBeMeasured(pod *corev1.Pod) bool {
	if m.percentageMeasured <= 0 {
		return false
	}
	// Check if label is already set (for webhook idempotency)
	if pod.Labels != nil {
		if value, exists := pod.Labels[measuredPodLabel]; exists {
			return value == "true"
		}
	}
	// Use true randomness - each pod gets a chance to be measured
	chance := rand.Float64() * 100
	return chance < m.percentageMeasured
}

// markPodAsMeasured adds the measured label to the pod
func (m *podMutator) markPodAsMeasured(pod *corev1.Pod, logger *logrus.Entry) {
	m.setMeasuredLabel(pod, true, logger)
}

// setMeasuredLabel sets the measured label to "true" or "false" for idempotency
func (m *podMutator) setMeasuredLabel(pod *corev1.Pod, measured bool, logger *logrus.Entry) {
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	if measured {
		pod.Labels[measuredPodLabel] = "true"
		logger.WithField("pod", pod.Name).Debugf("Marked pod as measured")
	} else {
		pod.Labels[measuredPodLabel] = "false"
	}
}

// increaseCPUForMeasuredPod increases CPU requests and limits for all containers in a measured pod by the configured percentage,
// with proper capping at the pod level (sum of all non-init containers)
func increaseCPUForMeasuredPod(pod *corev1.Pod, nodeCache *nodeAllocatableCache, cpuIncreasePercent float64, logger *logrus.Entry) {
	// Calculate total CPU request for all non-init containers
	type containerCPUInfo struct {
		index         int
		originalCPU   resource.Quantity
		originalLimit *resource.Quantity
		originalName  string
	}

	var containerInfos []containerCPUInfo
	var totalCPU float64

	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Resources.Requests == nil {
			continue
		}
		cpuRequest, ok := pod.Spec.Containers[i].Resources.Requests[corev1.ResourceCPU]
		if !ok {
			continue
		}
		cpuValue := cpuRequest.AsApproximateFloat64()
		totalCPU += cpuValue

		var cpuLimit *resource.Quantity
		if pod.Spec.Containers[i].Resources.Limits != nil {
			if limit, hasLimit := pod.Spec.Containers[i].Resources.Limits[corev1.ResourceCPU]; hasLimit {
				cpuLimit = &limit
			}
		}

		containerInfos = append(containerInfos, containerCPUInfo{
			index:         i,
			originalCPU:   cpuRequest,
			originalLimit: cpuLimit,
			originalName:  pod.Spec.Containers[i].Name,
		})
	}

	if totalCPU == 0 || len(containerInfos) == 0 {
		return
	}

	// Increase by configured percentage
	multiplier := 1.0 + (cpuIncreasePercent / 100.0)
	increasedTotalCPU := totalCPU * multiplier

	// Get workload class from pod labels to determine max CPU
	workloadClass := pod.Labels[ciWorkloadLabel]
	maxCPU := nodeCache.getMaxCPUForWorkload(workloadClass)

	// Cap at the maximum allocatable CPU for the workload type (pod-level cap)
	var scaleFactor float64 = 1.0
	if increasedTotalCPU > float64(maxCPU) {
		scaleFactor = float64(maxCPU) / increasedTotalCPU
		logger.Debugf("Capped increased CPU to %d cores total (workload class: %s), scaling factor: %.2f", maxCPU, workloadClass, scaleFactor)
	}

	// Apply proportional scaling to all containers (both requests and limits)
	for _, info := range containerInfos {
		originalValue := info.originalCPU.AsApproximateFloat64()
		increasedValue := originalValue * multiplier * scaleFactor
		newCPU := *resource.NewMilliQuantity(int64(increasedValue*1000), resource.DecimalSI)
		pod.Spec.Containers[info.index].Resources.Requests[corev1.ResourceCPU] = newCPU

		// Also increase CPU limits proportionally if they exist
		if info.originalLimit != nil {
			originalLimitValue := info.originalLimit.AsApproximateFloat64()
			increasedLimitValue := originalLimitValue * multiplier * scaleFactor
			newCPULimit := *resource.NewMilliQuantity(int64(increasedLimitValue*1000), resource.DecimalSI)
			// Ensure limit >= request (Kubernetes requirement)
			if newCPULimit.Cmp(newCPU) < 0 {
				newCPULimit = newCPU
			}
			if pod.Spec.Containers[info.index].Resources.Limits == nil {
				pod.Spec.Containers[info.index].Resources.Limits = make(corev1.ResourceList)
			}
			pod.Spec.Containers[info.index].Resources.Limits[corev1.ResourceCPU] = newCPULimit
			logger.Debugf("Container %s: increased CPU request from %s to %s, limit from %s to %s", info.originalName, info.originalCPU.String(), newCPU.String(), info.originalLimit.String(), newCPULimit.String())
		} else {
			logger.Debugf("Container %s: increased CPU request from %s to %s", info.originalName, info.originalCPU.String(), newCPU.String())
		}
	}
}

// addMeasuredPodAntiaffinity adds anti-affinity rules for measured pods
func (m *podMutator) addMeasuredPodAntiaffinity(pod *corev1.Pod, logger *logrus.Entry) {
	if pod.Spec.Affinity == nil {
		pod.Spec.Affinity = &corev1.Affinity{}
	}
	if pod.Spec.Affinity.PodAntiAffinity == nil {
		pod.Spec.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}

	// Measured pods should not run with explicitly unmeasured pods.
	// We use In ["false"] rather than NotIn ["true"] because the NotIn operator
	// matches pods where the label is absent, not just pods where it has a different
	// value. Since DaemonSet pods (node-exporter, dns, csi drivers, etc.) do not
	// carry the measured label, NotIn ["true"] would match them on every node,
	// making measured pods unschedulable cluster-wide.
	// NamespaceSelector is set to an empty selector to match pods across ALL
	// namespaces, since CI pods from different jobs run in separate ci-op-* namespaces.
	// Without this, anti-affinity defaults to same-namespace only and is ineffective.
	antiAffinityTerm := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      measuredPodLabel,
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"false"},
				},
			},
		},
		NamespaceSelector: &metav1.LabelSelector{},
		TopologyKey:       "kubernetes.io/hostname",
	}

	// Use required anti-affinity to force node scaling if no viable node exists
	pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
		pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
		antiAffinityTerm,
	)

	logger.Debugf("Added anti-affinity for measured pod against non-measured pods")
}

// nodeAllocatableCache caches node allocatable CPU information
type nodeAllocatableCache struct {
	client            kubernetes.Interface
	lock              sync.RWMutex
	cache             map[string]int64
	lastUpdate        time.Time
	systemReservedCPU int64
}

const (
	nodeCacheRefreshInterval = 15 * time.Minute
	maxMeasuredCPUCores      = 10
)

func newNodeAllocatableCache(client kubernetes.Interface, systemReservedCPU int64) *nodeAllocatableCache {
	cache := &nodeAllocatableCache{
		client:            client,
		cache:             make(map[string]int64),
		systemReservedCPU: systemReservedCPU,
	}

	// Initial load
	cache.refresh()

	// Periodic refresh
	interrupts.TickLiteral(cache.refresh, nodeCacheRefreshInterval)

	return cache
}

// refresh updates the cache with current node allocatable CPU information
func (c *nodeAllocatableCache) refresh() {
	logger := logrus.WithField("component", "node-allocatable-cache")
	logger.Debug("Refreshing node allocatable CPU cache")

	nodes, err := c.client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		logger.WithError(err).Warn("Failed to list nodes for allocatable CPU cache")
		return
	}

	newCache := make(map[string]int64)

	// Group nodes by workload type (ci-workload label)
	workloadNodes := make(map[string][]*corev1.Node)
	for i := range nodes.Items {
		node := &nodes.Items[i]
		workloadType := node.Labels[ciWorkloadLabel]
		if workloadType == "" {
			continue
		}
		workloadNodes[workloadType] = append(workloadNodes[workloadType], node)
	}

	// Find minimum allocatable CPU per workload type, minus system reserve, capped at maxMeasuredCPUCores.
	for workloadType, nodeList := range workloadNodes {
		minAllocatable := int64(0)
		for _, node := range nodeList {
			cpu := node.Status.Allocatable[corev1.ResourceCPU]
			cpuCores := int64(cpu.AsApproximateFloat64())

			if minAllocatable == 0 || cpuCores < minAllocatable {
				minAllocatable = cpuCores
			}
		}

		minAllocatable -= c.systemReservedCPU
		if minAllocatable < 0 {
			minAllocatable = 0
		}
		if minAllocatable > maxMeasuredCPUCores {
			minAllocatable = maxMeasuredCPUCores
		}

		newCache[workloadType] = minAllocatable
		logger.Debugf("Workload type %s: min allocatable CPU = %d cores (after %d core system reserve)", workloadType, minAllocatable, c.systemReservedCPU)
	}

	c.lock.Lock()
	c.cache = newCache
	c.lastUpdate = time.Now()
	c.lock.Unlock()

	logger.Debug("Node allocatable CPU cache refreshed")
}

// getMaxCPUForWorkload returns the maximum CPU (in cores) for a given workload type
func (c *nodeAllocatableCache) getMaxCPUForWorkload(workloadType string) int64 {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if maxCPU, ok := c.cache[workloadType]; ok {
		return maxCPU
	}

	return maxMeasuredCPUCores
}

type escalationServer struct {
	cache  Cache
	logger *logrus.Entry
	lock   sync.RWMutex
	index  podscaler.EscalationIndex
	factor float64
}

func newEscalationServer(cache Cache, factor float64) *escalationServer {
	s := &escalationServer{
		cache:  cache,
		logger: logrus.WithField("component", "pod-scaler escalation"),
		index:  podscaler.EscalationIndex{},
		factor: factor,
	}
	s.reload()
	interrupts.TickLiteral(s.reload, time.Hour)
	return s
}

func loadEscalationIndex(cache Cache, logger *logrus.Entry) podscaler.EscalationIndex {
	data, err := loadFrom(cache, podscaler.EscalationsCacheName)
	if err != nil {
		if _, ok := err.(notExist); ok {
			return podscaler.EscalationIndex{}
		}
		logger.WithError(err).Warn("Failed to load escalation index, starting fresh.")
		return podscaler.EscalationIndex{}
	}
	index := podscaler.EscalationIndex{}
	if err := json.Unmarshal(data, &index); err != nil {
		logger.WithError(err).Warn("Failed to parse escalation index, starting fresh.")
		return podscaler.EscalationIndex{}
	}
	return index
}

func (s *escalationServer) reload() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.index = loadEscalationIndex(s.cache, s.logger)
}

func (s *escalationServer) levels(workloadType, workloadName string) (memoryLevel, cpuLevel int) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	state, ok := s.index[podscaler.WorkloadKey(workloadType, workloadName)]
	if !ok {
		return 0, 0
	}
	return state.MemoryLevel, state.CPULevel
}

func (s *escalationServer) scaleQuantity(q resource.Quantity, level int) resource.Quantity {
	if level <= 0 {
		return q
	}
	multiplier := math.Pow(s.factor, float64(level))
	scaled := q.AsApproximateFloat64() * multiplier
	if q.Format == resource.DecimalSI {
		return *resource.NewMilliQuantity(int64(scaled*1000), resource.DecimalSI)
	}
	return *resource.NewQuantity(int64(scaled), q.Format)
}

func applyFailureEscalation(resources *corev1.ResourceRequirements, workloadType, workloadName string, server *escalationServer, logger *logrus.Entry) {
	if server == nil || resources == nil {
		return
	}
	memoryLevel, cpuLevel := server.levels(workloadType, workloadName)
	if memoryLevel == 0 && cpuLevel == 0 {
		return
	}
	if resources.Requests == nil {
		resources.Requests = corev1.ResourceList{}
	}
	if resources.Limits == nil {
		resources.Limits = corev1.ResourceList{}
	}
	if cpuLevel > 0 {
		if q, ok := resources.Requests[corev1.ResourceCPU]; ok {
			resources.Requests[corev1.ResourceCPU] = server.scaleQuantity(q, cpuLevel)
		}
		if q, ok := resources.Limits[corev1.ResourceCPU]; ok {
			resources.Limits[corev1.ResourceCPU] = server.scaleQuantity(q, cpuLevel)
		}
	}
	if memoryLevel > 0 {
		if q, ok := resources.Requests[corev1.ResourceMemory]; ok {
			resources.Requests[corev1.ResourceMemory] = server.scaleQuantity(q, memoryLevel)
		}
		if q, ok := resources.Limits[corev1.ResourceMemory]; ok {
			resources.Limits[corev1.ResourceMemory] = server.scaleQuantity(q, memoryLevel)
		}
	}
	logger.WithFields(logrus.Fields{
		"workloadType": workloadType,
		"workloadName": workloadName,
		"memory_level": memoryLevel,
		"cpu_level":    cpuLevel,
	}).Debug("applied failure escalation multiplier")
}

func storeEscalationIndex(cache Cache, index podscaler.EscalationIndex) error {
	raw, err := json.Marshal(index)
	if err != nil {
		return err
	}
	var lastErr error
	for i := 0; i < 5; i++ {
		storeErr := storeTo(cache, podscaler.EscalationsCacheName, raw)
		if storeErr == nil {
			return nil
		}
		if errors.Is(storeErr, context.DeadlineExceeded) {
			lastErr = storeErr
			continue
		}
		return storeErr
	}
	if lastErr != nil {
		return lastErr
	}
	return context.DeadlineExceeded
}

func applyAuthoritativeLimitDecrease(recommended, configured *corev1.ResourceRequirements, workloadName, workloadType string, isMeasured bool, workloadClass string, authoritative authoritativeConfig, authoritativeDecreaseUsage authoritativeDecreaseUsageBasis, escalations *escalationServer, logger *logrus.Entry) {
	if isMeasured {
		return
	}
	memoryLevel, cpuLevel := 0, 0
	if escalations != nil {
		memoryLevel, cpuLevel = escalations.levels(workloadType, workloadName)
	}
	for _, target := range []struct {
		configuredList *corev1.ResourceList
		resourceType   string
	}{
		{configuredList: &configured.Requests, resourceType: "request"},
		{configuredList: &configured.Limits, resourceType: "limit"},
	} {
		if target.configuredList == nil {
			continue
		}
		for _, field := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			if field == corev1.ResourceCPU && cpuLevel > 0 {
				continue
			}
			if field == corev1.ResourceMemory && memoryLevel > 0 {
				continue
			}
			determined, ok := usageForAuthoritativeDecrease(*recommended, field, authoritativeDecreaseUsage)
			if !ok {
				continue
			}
			if field == corev1.ResourceCPU {
				determined.SetMilli(int64(float64(determined.MilliValue()) * 1.2))
			} else {
				increased := determined.AsApproximateFloat64() * 1.2
				determined.Set(int64(increased))
			}
			configuredValue, ok := (*target.configuredList)[field]
			if !ok || configuredValue.IsZero() {
				continue
			}
			mode := authoritative.pair(field, target.resourceType)
			if !mode.apply && !mode.dryRun {
				continue
			}
			fieldLogger := logger.WithFields(logrus.Fields{
				"workloadName": workloadName,
				"workloadType": workloadType,
				"field":        field,
				"resource":     target.resourceType,
				"determined":   determined.String(),
				"configured":   configuredValue.String(),
			})
			if determined.Cmp(configuredValue) >= 0 {
				continue
			}
			configuredFloat := configuredValue.AsApproximateFloat64()
			determinedFloat := determined.AsApproximateFloat64()
			if 1.0-(determinedFloat/configuredFloat) < 0.05 {
				continue
			}
			reductionCapped := false
			if 1.0-(determinedFloat/configuredFloat) > mode.maxReduction {
				switch field {
				case corev1.ResourceCPU:
					determined.SetMilli(int64(float64(configuredValue.MilliValue()) * (1.0 - mode.maxReduction)))
				case corev1.ResourceMemory:
					determined.Set(int64(configuredFloat * (1.0 - mode.maxReduction)))
				}
				reductionCapped = true
			}
			if field == corev1.ResourceCPU {
				if determined.Cmp(authoritativeMinCPURequest) < 0 {
					determined = authoritativeMinCPURequest
				}
				if determined.Cmp(configuredValue) >= 0 {
					continue
				}
			}
			if mode.dryRun {
				fieldLogger.WithFields(logrus.Fields{
					"event":            "authoritative_decrease_dry_run",
					"workloadClass":    workloadClass,
					"would_set":        determined.String(),
					"authoritative":    mode.apply,
					"reduction_pct":    (1.0 - determined.AsApproximateFloat64()/configuredFloat) * 100,
					"reduction_capped": reductionCapped,
				}).Infof("authoritative %s decrease dry-run", target.resourceType)
				continue
			}
			fieldLogger.WithFields(logrus.Fields{
				"event":            "authoritative_decrease_applied",
				"workloadClass":    workloadClass,
				"set_to":           determined.String(),
				"reduction_pct":    (1.0 - determined.AsApproximateFloat64()/configuredFloat) * 100,
				"reduction_capped": reductionCapped,
			}).Infof("authoritative %s decrease applied", target.resourceType)
			(*target.configuredList)[field] = determined
		}
	}
}

func clampRequestsToLimits(resources *corev1.ResourceRequirements) {
	if resources == nil || resources.Limits == nil || resources.Requests == nil {
		return
	}
	for _, field := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		limit, ok := resources.Limits[field]
		if !ok || limit.IsZero() {
			continue
		}
		request, ok := resources.Requests[field]
		if !ok || request.IsZero() {
			continue
		}
		if request.Cmp(limit) > 0 {
			resources.Requests[field] = limit.DeepCopy()
		}
	}
}
