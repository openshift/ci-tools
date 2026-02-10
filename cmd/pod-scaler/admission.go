package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
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
	"github.com/openshift/ci-tools/pkg/util"
)

func admit(port, healthPort int, certDir string, client buildclientv1.BuildV1Interface, loaders map[string][]*cacheReloader, mutateResourceLimits bool, cpuCap int64, memoryCap string, cpuPriorityScheduling int64, percentageMeasured float64, measuredPodCPUIncrease float64, reporter results.PodScalerReporter) {
	logger := logrus.WithField("component", "pod-scaler admission")
	logger.Infof("Initializing admission webhook server with %d loaders.", len(loaders))
	health := pjutil.NewHealthOnPort(healthPort)
	resources := newResourceServer(loaders, health)
	decoder := admission.NewDecoder(scheme.Scheme)

	// Initialize node allocatable CPU cache
	restConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load cluster config for node cache.")
	}
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create Kubernetes client for node cache.")
	}
	nodeCache := newNodeAllocatableCache(kubeClient)

	server := webhook.NewServer(webhook.Options{
		Port:    port,
		CertDir: certDir,
	})
	server.Register("/pods", &webhook.Admission{Handler: &podMutator{logger: logger, client: client, decoder: decoder, resources: resources, mutateResourceLimits: mutateResourceLimits, cpuCap: cpuCap, memoryCap: memoryCap, cpuPriorityScheduling: cpuPriorityScheduling, percentageMeasured: percentageMeasured, measuredPodCPUIncrease: measuredPodCPUIncrease, nodeCache: nodeCache, reporter: reporter}})
	logger.Info("Serving admission webhooks.")
	if err := server.Start(interrupts.Context()); err != nil {
		logrus.WithError(err).Fatal("Failed to serve webhooks.")
	}
}

type podMutator struct {
	logger                 *logrus.Entry
	client                 buildclientv1.BuildV1Interface
	resources              *resourceServer
	mutateResourceLimits   bool
	decoder                admission.Decoder
	cpuCap                 int64
	memoryCap              string
	cpuPriorityScheduling  int64
	percentageMeasured     float64
	measuredPodCPUIncrease float64
	nodeCache              *nodeAllocatableCache
	reporter               results.PodScalerReporter
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

	mutatePodResources(pod, m.resources, m.mutateResourceLimits, m.cpuCap, m.memoryCap, isMeasured, m.nodeCache, m.measuredPodCPUIncrease, m.reporter, logger)
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

// useOursIfLarger updates fields in theirs when ours are larger
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
				fieldLogger.Debug("determined amount larger than configured")
				(*pair.theirs)[field] = our
				if their.Value() > 0 && our.Value() > (their.Value()*10) {
					reporter.ReportResourceConfigurationWarning(workloadName, workloadType, their.String(), our.String(), field.String(), isMeasured, workloadClass)
				}
			} else if cmp < 0 {
				fieldLogger.Debug("determined amount smaller than configured")
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

func mutatePodResources(pod *corev1.Pod, server *resourceServer, mutateResourceLimits bool, cpuCap int64, memoryCap string, isMeasured bool, nodeCache *nodeAllocatableCache, measuredPodCPUIncrease float64, reporter results.PodScalerReporter, logger *logrus.Entry) {
	// Set measured and workload class in metadata
	workloadClass := pod.Labels[ciWorkloadLabel]

	mutateResources := func(containers []corev1.Container) {
		for i := range containers {
			meta := podscaler.MetadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, containers[i].Name)
			meta.WorkloadClass = workloadClass

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

				// Take maximum CPU and memory from both
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
	measuredPodLabel = "pod-scaler.openshift.io/measured"
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

	// Measured pods should not run with non-measured pods
	// But measured pods can run together (no anti-affinity between measured pods)
	antiAffinityTerm := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      measuredPodLabel,
					Operator: metav1.LabelSelectorOpNotIn,
					Values:   []string{"true"},
				},
			},
		},
		TopologyKey: "kubernetes.io/hostname",
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
	client     kubernetes.Interface
	lock       sync.RWMutex
	cache      map[string]int64 // workload type -> max allocatable CPU (in cores)
	lastUpdate time.Time
}

const nodeCacheRefreshInterval = 15 * time.Minute

func newNodeAllocatableCache(client kubernetes.Interface) *nodeAllocatableCache {
	cache := &nodeAllocatableCache{
		client: client,
		cache:  make(map[string]int64),
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

	// Find minimum allocatable CPU for each workload type
	// Note: Allocatable already accounts for system/kubelet reserved resources
	for workloadType, nodeList := range workloadNodes {
		minAllocatable := int64(0)
		for _, node := range nodeList {
			cpu := node.Status.Allocatable[corev1.ResourceCPU]
			// AsApproximateFloat64() returns the value in cores
			cpuCores := int64(cpu.AsApproximateFloat64())

			if minAllocatable == 0 || cpuCores < minAllocatable {
				minAllocatable = cpuCores
			}
		}

		// Cap at 10 cores maximum
		if minAllocatable > 10 {
			minAllocatable = 10
		}

		if minAllocatable > 0 {
			newCache[workloadType] = minAllocatable
			logger.Debugf("Workload type %s: min allocatable CPU = %d cores", workloadType, minAllocatable)
		}
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

	// Default to 10 cores if workload type not found
	return 10
}
