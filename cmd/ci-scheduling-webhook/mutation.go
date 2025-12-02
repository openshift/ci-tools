package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	CiBuildNameLabelName        = "openshift.io/build.name"
	CiNamepsace                 = "ci"
	CiCreatedByProwLabelName    = "created-by-prow"
	KubernetesHostnameLabelName = "kubernetes.io/hostname"
)

var (
	// Non "openshift-*" namespace that need safe-to-evict
	safeToEvictNamespace = map[string]bool{
		"rh-corp-logging": true,
		"ocp":             true,
		"cert-manager":    true,
	}
	memoryThreshold = resource.MustParse("32Gi")
	cpuThreshold    = resource.MustParse("13")
)

func admissionReviewFromRequest(r *http.Request, deserializer runtime.Decoder) (*admissionv1.AdmissionReview, error) {
	// Validate that the incoming content type is correct.
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, fmt.Errorf("expected application/json content-type")
	}

	// Get the body data, which will be the AdmissionReview
	// content for the request.
	var body []byte
	if r.Body != nil {
		requestData, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		body = requestData
	}

	// Decode the request body into
	admissionReviewRequest := &admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, admissionReviewRequest); err != nil {
		return nil, err
	}

	return admissionReviewRequest, nil
}

func mutatePod(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	lastProfileTime := &start

	deserializer := codecs.UniversalDeserializer()

	writeHttpError := func(statusCode int, err error) {
		msg := fmt.Sprintf("error during mutation operation: %v", err)
		klog.Error(msg)
		w.WriteHeader(statusCode)
		_, err = w.Write([]byte(msg))
		if err != nil {
			klog.Errorf("Unable to return http error response to caller: %v", err)
		}
	}

	// Parse the AdmissionReview from the http request.
	admissionReviewRequest, err := admissionReviewFromRequest(r, deserializer)
	if err != nil {
		writeHttpError(400, fmt.Errorf("error getting admission review from request: %w", err))
		return
	}

	nodeResource := metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	if admissionReviewRequest.Request == nil || admissionReviewRequest.Request.Resource == nodeResource {
		mutateNode(admissionReviewRequest, w)
		return
	}

	// Do server-side validation that we are only dealing with a pod resource. This
	// should also be part of the MutatingWebhookConfiguration in the cluster, but
	// we should verify here before continuing.
	podResource := metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	if admissionReviewRequest.Request == nil || admissionReviewRequest.Request.Resource != podResource {
		writeHttpError(400, fmt.Errorf("did not receive pod, got %v", admissionReviewRequest.Request))
		return
	}

	// Decode the pod from the AdmissionReview.
	rawRequest := admissionReviewRequest.Request.Object.Raw
	pod := corev1.Pod{}
	if _, _, err := deserializer.Decode(rawRequest, nil, &pod); err != nil {
		writeHttpError(500, fmt.Errorf("error decoding raw pod: %w", err))
		return
	}

	profile := func(action string) {
		duration := time.Since(start)
		sinceLastProfile := time.Since(*lastProfileTime)
		now := time.Now()
		lastProfileTime = &now
		klog.Infof("mutate-pod [%v] [%v] within (ms): %v [diff %v]", admissionReviewRequest.Request.UID, action, duration.Milliseconds(), sinceLastProfile.Milliseconds())
	}

	profile("decoded request")

	podClass := PodClassNone // will be set to CiWorkloadLabelValueBuilds or CiWorkloadLabelValueTests depending on analysis

	patchEntries := make([]map[string]interface{}, 0)
	addPatchEntry := func(op string, path string, value interface{}) {
		patch := map[string]interface{}{
			"op":    op,
			"path":  path,
			"value": value,
		}
		patchEntries = append(patchEntries, patch)
	}

	podName := admissionReviewRequest.Request.Name
	namespace := admissionReviewRequest.Request.Namespace

	// OSD has so. many. operator related resources which aren't using replicasets.
	// These are normally unevictable and thus prevent the autoscaler from considering
	// a node for deletion. We mark them evictable.
	// OSD operator catalogs are presently unevictable, so do those wherever we find them.
	_, needsEvitable := safeToEvictNamespace[namespace]
	if strings.HasPrefix(namespace, "openshift-") || strings.Contains(podName, "-catalog-") || needsEvitable {
		annotations := pod.Annotations
		if annotations == nil {
			annotations = make(map[string]string, 0)
		}
		annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"] = "true"
		addPatchEntry("add", "/metadata/annotations", annotations)
	}

	if namespace == CiNamepsace {
		if _, ok := pod.Labels[CiCreatedByProwLabelName]; ok {
			// if we are in 'ci' and created by prow, this the direct prowjob pod. Treat as test.
			podClass = PodClassProwJobs
		}
	}

	labels := pod.Labels
	if labels == nil {
		labels = make(map[string]string, 0)
	}

	if strings.HasPrefix(namespace, "ci-op-") || strings.HasPrefix(namespace, "ci-ln-") {
		skipPod := false // certain pods with special resources we can schedule onto our workload sets

		checkForSpecialContainers := func(containers []corev1.Container) {
			for i := range containers {
				c := &containers[i]
				for key := range c.Resources.Requests {
					if key != corev1.ResourceCPU && key != corev1.ResourceMemory && key != corev1.ResourceEphemeralStorage {
						// There is a special resource required - avoid trying to schedule it with build/test machinesets
						skipPod = true
					}
				}
			}
		}

		checkForSpecialContainers(pod.Spec.InitContainers)
		checkForSpecialContainers(pod.Spec.Containers)

		if !skipPod {
			if _, ok := labels[CiBuildNameLabelName]; ok {
				podClass = PodClassBuilds
			} else {
				podClass = PodClassTests
			}
		}
	}

	klog.Infof("Pod %s in namespace %s is classified as %s", podName, namespace, podClass)

	// Add NET_ADMIN and NET_RAW capabilities to test containers that require them
	if podClass == PodClassTests {
		// Check each container for the environment variable
		for i, container := range pod.Spec.Containers {
			if container.Name == "test" {
				// Check if this container has TEST_REQUIRES_BUILDFARM_NET_ADMIN=true
				requiresNetAdmin := false
				for _, env := range container.Env {
					if env.Name == "TEST_REQUIRES_BUILDFARM_NET_ADMIN" && env.Value == "true" {
						requiresNetAdmin = true
						break
					}
				}

				if requiresNetAdmin {
					// Build the correct patch based on existing securityContext
					if container.SecurityContext == nil {
						// No securityContext exists, create one with capabilities
						container.SecurityContext = &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_ADMIN", "NET_RAW", "SETUID", "SETGID"},
							},
						}
					} else if container.SecurityContext.Capabilities == nil {
						// securityContext exists but no capabilities, add capabilities
						container.SecurityContext.Capabilities = &corev1.Capabilities{
							Add: []corev1.Capability{"NET_ADMIN", "NET_RAW", "SETUID", "SETGID"},
						}
					} else {
						// Both securityContext and capabilities exist, merge the "add" array
						container.SecurityContext.Capabilities.Add = append(container.SecurityContext.Capabilities.Add, "NET_ADMIN", "NET_RAW", "SETUID", "SETGID")
					}
					container.SecurityContext.RunAsUser = ptr.To(int64(0))
					container.SecurityContext.RunAsNonRoot = ptr.To(false)

					addPatchEntry("replace", fmt.Sprintf("/spec/containers/%d/securityContext", i), container.SecurityContext)
					klog.Infof("Added NET_ADMIN, NET_RAW, SETUID, and SETGID capabilities, ensured runAsUser=0 and allowPrivilegeEscalation=true for test container in pod %s in namespace %s due to TEST_REQUIRES_BUILDFARM_NET_ADMIN=true", podName, namespace)
				}
				break
			}
		}
	}

	if podClass == PodClassTests {
		// Segmenting long run tests onto their own node set helps normal tests nodes scale down
		// more effectively.
		if strings.HasPrefix(podName, "release-images-") ||
			strings.HasPrefix(podName, "release-analysis-aggregator-") ||
			strings.HasPrefix(podName, "e2e-aws-upgrade") ||
			strings.HasPrefix(podName, "rpm-repo") ||
			strings.HasPrefix(podName, "osde2e-stage") ||
			strings.HasPrefix(podName, "e2e-aws-cnv") ||
			strings.Contains(podName, "ovn-upgrade-ipi") ||
			strings.Contains(podName, "ovn-upgrade-ovn") ||
			strings.Contains(podName, "ovn-upgrade-openshift-e2e-test") {
			podClass = PodClassLongTests
		}
	}

	if podClass != PodClassNone {
		profile("classified request")

		// Setup labels we might want to use in the future to set pod affinity
		labels[CiWorkloadLabelName] = string(podClass)
		labels[CiWorkloadNamespaceLabelName] = namespace

		// Ensure build pods request at least the configured minimum cores for docker-build containers
		if podClass == PodClassBuilds && minBuildMillicores > 0 {
			for i := range pod.Spec.Containers {
				c := &pod.Spec.Containers[i]

				// Only apply to docker-build containers
				if c.Name != "docker-build" {
					continue
				}

				cpuRequest, hasCPURequest := c.Resources.Requests[corev1.ResourceCPU]
				cpuLimit, hasCPULimit := c.Resources.Limits[corev1.ResourceCPU]

				// Handle CPU requests
				if !hasCPURequest || cpuRequest.MilliValue() < minBuildMillicores {
					// Preserve all existing resource requests
					newRequests := make(map[string]interface{})
					for resourceName, quantity := range c.Resources.Requests {
						newRequests[string(resourceName)] = quantity.String()
					}
					// Set/override CPU to minimum
					newRequests[string(corev1.ResourceCPU)] = fmt.Sprintf("%dm", minBuildMillicores)

					addPatchEntry("add", fmt.Sprintf("/spec/containers/%d/resources/requests", i), newRequests)
					if hasCPURequest {
						klog.Infof("Increasing build pod CPU request from %vm to %vm for container %s", cpuRequest.MilliValue(), minBuildMillicores, c.Name)
					} else {
						klog.Infof("Setting build pod CPU request to %vm for container %s", minBuildMillicores, c.Name)
					}
				}

				// Handle CPU limits - only if they exist and are below minimum
				if hasCPULimit && cpuLimit.MilliValue() < minBuildMillicores {
					// Preserve all existing resource limits
					newLimits := make(map[string]interface{})
					for resourceName, quantity := range c.Resources.Limits {
						newLimits[string(resourceName)] = quantity.String()
					}
					// Set/override CPU to minimum
					newLimits[string(corev1.ResourceCPU)] = fmt.Sprintf("%dm", minBuildMillicores)

					addPatchEntry("add", fmt.Sprintf("/spec/containers/%d/resources/limits", i), newLimits)
					klog.Infof("Increasing build pod CPU limit from %vm to %vm for container %s", cpuLimit.MilliValue(), minBuildMillicores, c.Name)
				}
			}
		}

		// Setup toleration appropriate for podClass so that it can only land on desired machineset.
		// This is achieved by virtue of using a RuntimeClass object which specifies the necessary
		// tolerations for each workload.
		addPatchEntry("add", "/spec/runtimeClassName", "ci-scheduler-runtime-"+podClass)

		// Set a nodeSelector to ensure this finds our desired machineset nodes
		nodeSelector := pod.Spec.NodeSelector
		if nodeSelector == nil {
			nodeSelector = make(map[string]string)
		}
		nodeSelector[CiWorkloadLabelName] = string(podClass)
		addPatchEntry("add", "/spec/nodeSelector", nodeSelector)

		precludedHostnames := prioritization.findHostnamesToPreclude(podClass)

		affinity := corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{},
		}
		affinityChanged := false
		if err == nil {
			if len(precludedHostnames) > 0 {
				// Use MatchExpressions here because MatchFields because MatchExpressions
				// only allows one value in the Values list.
				requiredNoSchedulingSelector := corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      KubernetesHostnameLabelName,
									Operator: "NotIn",
									Values:   precludedHostnames,
								},
							},
						},
					},
				}
				affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &requiredNoSchedulingSelector
				affinityChanged = true
			}
		} else {
			klog.Errorf("No node precludes will be set in pod due to error: %v", err)
		}

		highPerfPod := false
		if podClass == PodClassBuilds {
			// Use high performance nodes for large pods
			for _, container := range pod.Spec.Containers {
				if container.Resources.Requests.Memory().Cmp(memoryThreshold) >= 0 || container.Resources.Requests.Cpu().Cmp(cpuThreshold) >= 0 {
					klog.Infof("Pod %s in namespace %s requests high performance node", podName, namespace)
					highPerfPod = true
				}
			}
			// If this is a build pod, prefer to be scheduled to spot instances for cost efficiency.
			// If there are no spot instances, this will be ignored.
			affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.PreferredSchedulingTerm{
				{
					Weight: 100,
					Preference: corev1.NodeSelectorTerm{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								// Prefer spot.io instances that are actual spot instances
								Key:      "spotinst.io/node-lifecycle",
								Operator: "In",
								Values:   []string{"spot"},
							},
						},
					},
				},
			}
			affinityChanged = true
		}

		if highPerfPod {
			patchHighPerfPod(&pod, podName, namespace, addPatchEntry)
		}

		if affinityChanged {
			unstructuredAffinity, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&affinity)
			if err != nil {
				writeHttpError(500, fmt.Errorf("error decoding affinity to unstructured data: %w", err))
				return
			}

			addPatchEntry("add", "/spec/affinity", unstructuredAffinity)
		}

		// There is currently an issue with cluster scale up where pods are stacked up, unschedulable.
		// A machine is provisioned. As soon as the machine is provisioned, pods are scheduled to the
		// node and they begin to run before DNS daemonset pods can successfully configure the pod.
		// These leads to issues like being unable to resolve github.com in clonerefs.
		initContainers := pod.Spec.InitContainers
		if initContainers == nil {
			initContainers = make([]corev1.Container, 0)
		}

		initContainerName := "ci-scheduling-dns-wait"

		// This webhook supports reinvocation. Don't add an initContainer every time we are invoked.
		found := false
		for _, container := range initContainers {
			if container.Name == initContainerName {
				found = true
				break
			}
		}

		if !found {

			// We've found DNS issues with pods coming up and not being able
			// to resolve hosts. This initContainer is a workaround which
			// will poll for a successful DNS lookup to a file that should
			// always be available.
			delayInitContainer := []corev1.Container{
				{
					Name:  initContainerName,
					Image: "registry.access.redhat.com/ubi8",
					Command: []string{
						"/bin/sh",
						"-c",
						`declare -i T; until [[ "$ret" == "0" ]] || [[ "$T" -gt "120" ]]; do curl http://static.redhat.com/test/rhel-networkmanager.txt > /dev/null; ret=$?; sleep 1; let "T+=1"; done`,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("200Mi"),
						},
					},
				},
			}

			initContainersMap := map[string][]corev1.Container{
				"initContainers": append(delayInitContainer, initContainers...), // prepend sleep container
			}
			unstructedInitContainersMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&initContainersMap)
			if err != nil {
				writeHttpError(500, fmt.Errorf("error decoding initContainers to unstructured data: %w", err))
				return
			}

			addPatchEntry("replace", "/spec/initContainers", unstructedInitContainersMap["initContainers"])
		}

		addPatchEntry("add", "/metadata/labels", labels)
	}

	// Create a response that will add a label to the pod if it does
	// not already have a label with the key of "hello". In this case
	// it does not matter what the value is, as long as the key exists.
	admissionResponse := &admissionv1.AdmissionResponse{}
	patch := make([]byte, 0)
	patchType := admissionv1.PatchTypeJSONPatch

	if len(patchEntries) > 0 {
		marshalled, err := json.Marshal(patchEntries)
		if err != nil {
			klog.Errorf("Error marshalling JSON patch (%v) from: %v", patchEntries, err)
			writeHttpError(500, fmt.Errorf("error marshalling jsonpatch: %w", err))
			return
		}
		patch = marshalled
	}

	admissionResponse.Allowed = true
	if len(patch) > 0 {
		klog.InfoS("Incoming pod to be modified", "podClass", podClass, "pod", fmt.Sprintf("-n %v pod/%v", namespace, podName))
		admissionResponse.PatchType = &patchType
		admissionResponse.Patch = patch
	}

	// Construct the response, which is just another AdmissionReview.
	var admissionReviewResponse admissionv1.AdmissionReview
	admissionReviewResponse.Response = admissionResponse
	admissionReviewResponse.SetGroupVersionKind(admissionReviewRequest.GroupVersionKind())
	admissionReviewResponse.Response.UID = admissionReviewRequest.Request.UID

	resp, err := json.Marshal(admissionReviewResponse)
	if err != nil {
		writeHttpError(500, fmt.Errorf("error marshalling admission review response: %w", err))
		return
	}
	profile("ready to write response")

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(resp)
	if err != nil {
		klog.Errorf("Unable to respond to caller with admission review: %v", err)
	}
}

func patchHighPerfPod(pod *corev1.Pod, podName, namespace string, addPatchEntry func(string, string, interface{})) {
	klog.Infof("Pod %s in namespace %s is a high performance pod", podName, namespace)
	tolerations := pod.Spec.Tolerations
	tolerations = append(tolerations, corev1.Toleration{
		Key:      "ci-instance-type",
		Operator: corev1.TolerationOpEqual,
		Value:    "high-perf",
		Effect:   corev1.TaintEffectNoSchedule,
	})
	addPatchEntry("add", "/spec/tolerations", tolerations)

	if pod.Spec.NodeSelector == nil {
		pod.Spec.NodeSelector = make(map[string]string)
	}
	pod.Spec.NodeSelector["ci-instance-type"] = "high-perf"
	addPatchEntry("replace", "/spec/nodeSelector", pod.Spec.NodeSelector)
}

func mutateNode(admissionReviewRequest *admissionv1.AdmissionReview, w http.ResponseWriter) {
	start := time.Now()
	lastProfileTime := &start

	deserializer := codecs.UniversalDeserializer()

	writeHttpError := func(statusCode int, err error) {
		msg := fmt.Sprintf("error during mutation operation: %v", err)
		klog.Error(msg)
		w.WriteHeader(statusCode)
		_, err = w.Write([]byte(msg))
		if err != nil {
			klog.Errorf("Unable to return http error response to caller: %v", err)
		}
	}

	// Do server-side validation that we are only dealing with a pod resource. This
	// should also be part of the MutatingWebhookConfiguration in the cluster, but
	// we should verify here before continuing.
	nodeResource := metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	if admissionReviewRequest.Request == nil || admissionReviewRequest.Request.Resource != nodeResource {
		writeHttpError(400, fmt.Errorf("did not receive node, got %v", admissionReviewRequest.Request))
		return
	}

	// Decode the Node from the AdmissionReview.
	rawRequest := admissionReviewRequest.Request.Object.Raw
	node := corev1.Node{}
	if _, _, err := deserializer.Decode(rawRequest, nil, &node); err != nil {
		writeHttpError(500, fmt.Errorf("error decoding raw node: %w", err))
		return
	}

	profile := func(action string) {
		duration := time.Since(start)
		sinceLastProfile := time.Since(*lastProfileTime)
		now := time.Now()
		lastProfileTime = &now
		klog.Infof("mutate-node [%v] [%v] within (ms): %v [diff %v]", admissionReviewRequest.Request.UID, action, duration.Milliseconds(), sinceLastProfile.Milliseconds())
	}

	profile("decoded request")

	podClass := PodClassNone // will be set to CiWorkloadLabelValueBuilds or CiWorkloadLabelValueTests depending on analysis

	patchEntries := make([]map[string]interface{}, 0)
	addPatchEntry := func(op string, path string, value interface{}) {
		patch := map[string]interface{}{
			"op":    op,
			"path":  path,
			"value": value,
		}
		patchEntries = append(patchEntries, patch)
	}

	nodeName := admissionReviewRequest.Request.Name

	labels := node.Labels
	if labels != nil {
		if pc, ok := labels[CiWorkloadLabelName]; ok {
			podClass = PodClass(pc)
		}
	}

	if podClass != PodClassNone {
		profile("classified request")

		if _, ok := node.Annotations[NodeDisableScaleDownAnnotationKey]; !ok {
			// If this webhook owns this class of node, then we own its scale down in order to prevent
			// contention with the autoscaler. Ideally, we would apply this annotation declaratively
			// in the machineset, but it doesn't appear to support annotations. Instead,
			// we apply it on first sight of it not being present.
			// https://github.com/kubernetes/autoscaler/blob/a13c59c2430e5fe0e07d8233a536326394e0c925/cluster-autoscaler/FAQ.md#how-can-i-prevent-cluster-autoscaler-from-scaling-down-a-particular-node
			escapedKey := strings.ReplaceAll(NodeDisableScaleDownAnnotationKey, "/", "~1")
			addPatchEntry("add", "/metadata/annotations/"+escapedKey, "true")
		}
	}

	admissionResponse := &admissionv1.AdmissionResponse{}
	patch := make([]byte, 0)
	patchType := admissionv1.PatchTypeJSONPatch

	if len(patchEntries) > 0 {
		marshalled, err := json.Marshal(patchEntries)
		if err != nil {
			klog.Errorf("Error marshalling JSON patch (%v) from: %v", patchEntries, err)
			writeHttpError(500, fmt.Errorf("error marshalling jsonpatch: %w", err))
			return
		}
		patch = marshalled
	}

	admissionResponse.Allowed = true
	if len(patch) > 0 {
		klog.Info("Incoming node to be modified", "podClass", podClass, "node", nodeName)
		admissionResponse.PatchType = &patchType
		admissionResponse.Patch = patch
	}

	// Construct the response, which is just another AdmissionReview.
	var admissionReviewResponse admissionv1.AdmissionReview
	admissionReviewResponse.Response = admissionResponse
	admissionReviewResponse.SetGroupVersionKind(admissionReviewRequest.GroupVersionKind())
	admissionReviewResponse.Response.UID = admissionReviewRequest.Request.UID

	resp, err := json.Marshal(admissionReviewResponse)
	if err != nil {
		writeHttpError(500, fmt.Errorf("error marshalling admission review response: %w", err))
		return
	}
	profile("ready to write response")

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(resp)
	if err != nil {
		klog.Errorf("Unable to respond to caller with admission review: %v", err)
	}
}
