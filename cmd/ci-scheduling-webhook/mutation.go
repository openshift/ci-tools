package main

import (
	"fmt"
	"io/ioutil"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"net/http"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"encoding/json"
	"strings"
)

const (
	CiBuildNameLabelName = "openshift.io/build.name"
	CiNamepsace = "ci"
	CiCreatedByProwLabelName = "created-by-prow"
)

var (
	// Non "openshift-*" namespace that need safe-to-evict
	safeToEvictNamespace = map[string]bool {
		"rh-corp-logging": true,
		"ocp": true,
		"cert-manager": true,
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
	deserializer := codecs.UniversalDeserializer()

	writeHttpError := func(statusCode int, err error) {
		msg := fmt.Sprintf("error during mutation: %v", err)
		klog.Error(msg)
		w.WriteHeader(statusCode)
		w.Write([]byte(msg))
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
	if admissionReviewRequest.Request.Resource != podResource {
		writeHttpError(400, fmt.Errorf("did not receive pod, got %s", admissionReviewRequest.Request.Resource.Resource))
		return
	}

	// Decode the pod from the AdmissionReview.
	rawRequest := admissionReviewRequest.Request.Object.Raw
	pod := corev1.Pod{}
	if _, _, err := deserializer.Decode(rawRequest, nil, &pod); err != nil {
		writeHttpError(500, fmt.Errorf("error decoding raw pod: %v", err))
		return
	}

	podClass := ""  // will be set to CiWorkloadLabelValueBuilds or CiWorkloadLabelValueTests depending on analysis

	patchEntries := make([]map[string]interface{}, 0)
	addPatchEntry := func(op string, path string, value interface{}) {
		patch := map[string]interface{} {
			"op": op,
			"path": path,
			"value": value,
		}
		patchEntries = append(patchEntries, patch)
	}

	podName := pod.Name
	namespace := pod.Namespace

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
		addPatchEntry("add", "/metadata/annotations", annotations )
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
				podClass = CiWorkloadTestsTaintName
			}
		}
	}

	if podClass != "" {

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
						newRequests := map[string]interface{} {
							string(corev1.ResourceCPU): fmt.Sprintf("%vm", int64(float32(c.Resources.Requests.Cpu().MilliValue()) * factor)),
							string(corev1.ResourceMemory): fmt.Sprintf("%v", c.Resources.Requests.Memory().String()),
						}
						addPatchEntry("replace", fmt.Sprintf("/spec/%v/%v/resources/requests", containerType, i), newRequests)
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


		// Setup toleration appropriate for podClass so that it can only land on desired machineset
		podTolerations := pod.Spec.Tolerations
		var tolerationKey string
		if podClass == CiWorkloadLabelValueTests {
			tolerationKey = CiWorkloadTestsTaintName
		} else {
			tolerationKey = CiWorkloadBuildsTaintName
		}
		podTolerations = append(podTolerations, corev1.Toleration{
			Key:               tolerationKey,
			Operator:          "Exists",
			Effect:            "NoSchedule",
		})

		unstructuredTolerations, err := runtime.DefaultUnstructuredConverter.ToUnstructured(podTolerations)
		if err != nil {
			writeHttpError(500, fmt.Errorf("error decoding tolerations to unstructured data: %v", err))
			return
		}
		
		addPatchEntry("add", "/spec/tolerations", unstructuredTolerations)

		// Set a nodeSelector to ensure this finds our desired machineset nodes
		nodeSelector := make(map[string]string)
		nodeSelector[CiWorkloadLabelName] = podClass
		addPatchEntry("add", "/spec/nodeSelector", nodeSelector)

		// Set up a softNodeAffinity to try to deterministically schedule this Pod
		// on an ordered list of nodes. This will make it more likely that the
		// autoscaler will be find unused nodes (vs if the pods were spread
		// across all nodes evenly).

		mutex.Lock()
		var orderedNodeNames []string
		if podClass == CiWorkloadLabelValueTests {
			copy(orderedNodeNames, testsNodeNameList)
		} else {
			copy(orderedNodeNames, buildsNodeNameList)
		}
		mutex.Unlock()

		if len(orderedNodeNames) > 0 {
			preferredSchedulingTerms := make([]corev1.PreferredSchedulingTerm, len(orderedNodeNames))
			for i, nodeName := range orderedNodeNames {
				preferredSchedulingTerms = append(preferredSchedulingTerms,
					corev1.PreferredSchedulingTerm{
						Weight:     int32(i),
						Preference: corev1.NodeSelectorTerm{
							MatchFields: [] corev1.NodeSelectorRequirement {
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

			affinity := corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: preferredSchedulingTerms,
				},
			}

			unstructuredAffinity, err := runtime.DefaultUnstructuredConverter.ToUnstructured(affinity)
			if err != nil {
				writeHttpError(500, fmt.Errorf("error decoding affinity to unstructured data: %v", err))
				return
			}

			addPatchEntry("add", "/spec/affinity", unstructuredAffinity)
		}

	}



	// Create a response that will add a label to the pod if it does
	// not already have a label with the key of "hello". In this case
	// it does not matter what the value is, as long as the key exists.
	admissionResponse := &admissionv1.AdmissionResponse{}
	var patch string
	patchType := admissionv1.PatchTypeJSONPatch
	if _, ok := pod.Labels["hello"]; !ok {
		patch = `[{"op":"add","path":"/metadata/labels","value":{"hello":"world"}}]`
	}

	admissionResponse.Allowed = true
	if patch != "" {
		admissionResponse.PatchType = &patchType
		admissionResponse.Patch = []byte(patch)
	}

	// Construct the response, which is just another AdmissionReview.
	var admissionReviewResponse admissionv1.AdmissionReview
	admissionReviewResponse.Response = admissionResponse
	admissionReviewResponse.SetGroupVersionKind(admissionReviewRequest.GroupVersionKind())
	admissionReviewResponse.Response.UID = admissionReviewRequest.Request.UID

	resp, err := json.Marshal(admissionReviewResponse)
	if err != nil {
		msg := fmt.Sprintf("error marshalling response json: %v", err)
		logger.Printf(msg)
		w.WriteHeader(500)
		w.Write([]byte(msg))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}
