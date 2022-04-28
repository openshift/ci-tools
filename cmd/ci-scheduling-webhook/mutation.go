package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"net/http"
	"strings"
	"time"
)

const (
	CiBuildNameLabelName     = "openshift.io/build.name"
	CiNamepsace              = "ci"
	CiCreatedByProwLabelName = "created-by-prow"
)

var (
	// Non "openshift-*" namespace that need safe-to-evict
	safeToEvictNamespace = map[string]bool{
		"rh-corp-logging": true,
		"ocp":             true,
		"cert-manager":    true,
	}
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
		requestData, err := ioutil.ReadAll(r.Body)
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
		writeHttpError(400, fmt.Errorf("error getting admission review from request: %v", err))
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
		writeHttpError(500, fmt.Errorf("error decoding raw pod: %v", err))
		return
	}

	profile := func(action string) {
		duration := time.Since(start)
		sinceLastProfile := time.Since(*lastProfileTime)
		now := time.Now()
		lastProfileTime = &now
		klog.Infof("[%v] [%v] within (ms): %v [diff %v]", admissionReviewRequest.Request.UID, action, duration.Milliseconds(), sinceLastProfile.Milliseconds())
	}

	profile("decoded request")

	podClass := "" // will be set to CiWorkloadLabelValueBuilds or CiWorkloadLabelValueTests depending on analysis

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
			podClass = CiWorkloadLabelValueTests
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
				podClass = CiWorkloadLabelValueBuilds
			} else {
				podClass = CiWorkloadLabelValueTests
			}
		}
	}

	if podClass != "" {
		profile("classified request")

		// Setup labels we might want to use in the future to set pod affinity
		labels[CiWorkloadLabelName] = podClass
		labels[CiWorkloadNamespaceLabelName] = namespace

		addPatchEntry("add", "/metadata/labels", labels)

		// Reduce CPU requests, if appropriate
		reduceCPURequests := func(containerType string, containers []corev1.Container, factor float32) {
			if factor >= 1.0 {
				// Don't allow increases as this might inadvertently make the pod completely unschedulable.
				return
			}
			for i := range containers {
				c := &containers[i]
				// Limits are overcommited so there is no need to replace them. Besides, if they are set,
				// it will be good to preserve them so that the pods don't wildly overconsume.
				for key := range c.Resources.Requests {
					if key == corev1.ResourceCPU {
						// TODO : use convert to unstructured
						newRequests := map[string]interface{}{
							string(corev1.ResourceCPU):    fmt.Sprintf("%vm", int64(float32(c.Resources.Requests.Cpu().MilliValue())*factor)),
							string(corev1.ResourceMemory): fmt.Sprintf("%v", c.Resources.Requests.Memory().String()),
						}
						addPatchEntry("add", fmt.Sprintf("/spec/%v/%v/resources/requests", containerType, i), newRequests)
					}
				}
			}
		}
		var cpuFactor float32
		if podClass == CiWorkloadLabelValueTests {
			cpuFactor = shrinkTestCPU
		} else {
			cpuFactor = shrinkBuildCPU
		}
		reduceCPURequests("initContainers", pod.Spec.InitContainers, cpuFactor)
		reduceCPURequests("containers", pod.Spec.Containers, cpuFactor)

		// Setup toleration appropriate for podClass so that it can only land on desired machineset.
		// This is achieved by virtue of using a RuntimeClass object which specifies the necsesary
		// tolerations for each workload.
		addPatchEntry("add", "/spec/runtimeClassName", "ci-scheduler-runtime-"+podClass)

		// Set a nodeSelector to ensure this finds our desired machineset nodes
		nodeSelector := make(map[string]string)
		nodeSelector[CiWorkloadLabelName] = podClass
		addPatchEntry("add", "/spec/nodeSelector", nodeSelector)

		// Set up a softNodeAffinity to try to deterministically schedule this Pod
		// on an ordered list of nodes. This will make it more likely that the
		// autoscaler will be find unused nodes (vs if the pods were spread
		// across all nodes evenly).

		orderedNodeNames, excludedNodes, err := getNodeNamesInPreferredOrder(podClass, &pod)
		profile("assessed node fit")

		if err == nil {

			affinityChanged := false
			affinity := corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{},
			}

			if len(excludedNodes) > 0 {
				excludedHostnames := make([]string, 0)
				for i := range excludedNodes {
					excludedNode := excludedNodes[i]
					if hostname, ok := excludedNode.Labels[KubeNodeHostnameLabel]; ok {
						excludedHostnames = append(excludedHostnames, hostname)
					} else {
						klog.Warningf("Node %v does not have %v label set. Anti-affinity will not function.", excludedNode.Name, KubeNodeHostnameLabel)
					}
				}

				if len(excludedHostnames) > 0 {
					// Use MatchExpressions here because MatchFields because MatchExpressions
					// only allows one value in the Values list.
					requiredNoSchedulingSelector := corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      KubeNodeHostnameLabel,
										Operator: "NotIn",
										Values:   excludedHostnames,
									},
								},
							},
						},
					}
					affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &requiredNoSchedulingSelector
					affinityChanged = true
				}
			}

			if len(orderedNodeNames) > 0 {
				preferredSchedulingTerms := make([]corev1.PreferredSchedulingTerm, 0)
				for i, nodeName := range orderedNodeNames {
					if i == 5 {
						// 5 viable nodes should be sufficient for the scheduler to find a spot.
						// Avoid making the pod.spec huge.
						break
					}
					preferredSchedulingTerms = append(preferredSchedulingTerms,
						corev1.PreferredSchedulingTerm{
							Weight: int32(i) + 1,
							Preference: corev1.NodeSelectorTerm{
								MatchFields: []corev1.NodeSelectorRequirement{
									{
										Key:      "metadata.name",
										Operator: "In",
										Values:   []string{nodeName},
									},
								},
							},
						},
					)
				}

				affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = preferredSchedulingTerms
				affinityChanged = true
			}
			if affinityChanged {
				unstructuredAffinity, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&affinity)
				if err != nil {
					writeHttpError(500, fmt.Errorf("error decoding affinity to unstructured data: %v", err))
					return
				}

				addPatchEntry("add", "/spec/affinity", unstructuredAffinity)
			}
		} else {
			klog.Errorf("No node affinity will be set in pod due to error: %v", err)
		}
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
			klog.Errorf("Error marshalling JSON patch (%v) from: %v", patchEntries)
			writeHttpError(500, fmt.Errorf("error marshalling jsonpatch: %v", err))
			return
		}
		patch = marshalled
	}

	admissionResponse.Allowed = true
	if len(patch) > 0 {
		klog.InfoS("Incoming pod to be modified", "podClass", podClass, "namespace", namespace, "pod", podName)
		admissionResponse.PatchType = &patchType
		admissionResponse.Patch = patch
	} else {
		klog.InfoS("Incoming pod to be ignored", "podClass", podClass, "namespace", namespace, "pod", podName)
	}

	// Construct the response, which is just another AdmissionReview.
	var admissionReviewResponse admissionv1.AdmissionReview
	admissionReviewResponse.Response = admissionResponse
	admissionReviewResponse.SetGroupVersionKind(admissionReviewRequest.GroupVersionKind())
	admissionReviewResponse.Response.UID = admissionReviewRequest.Request.UID

	resp, err := json.Marshal(admissionReviewResponse)
	if err != nil {
		writeHttpError(500, fmt.Errorf("error marshalling admission review reponse: %v", err))
		return
	}
	profile("ready to write response")

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(resp)
	if err != nil {
		klog.Errorf("Unable to respond to caller with admission review: %v", err)
	}
}
