package main

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

func createOrUpdateDaemonSet(ctx context.Context, clientset *kubernetes.Clientset, daemonset *appsv1.DaemonSet) error {
	dsClient := clientset.AppsV1().DaemonSets(daemonset.Namespace)
	dsName := daemonset.Namespace + "/" + daemonset.Name
	_, err := dsClient.Get(ctx, daemonset.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		klog.Infof("Bootstrapping new daemonset for additional reserved memory: %v", dsName)
		_, err = dsClient.Create(ctx, daemonset, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to bootstrap daemonset for additional reserved memory %v: %v", dsName, err)
		}
	} else {
		// The daemonset exists, we just need to update it.
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Retrieve the latest version of Deployment before attempting update
			// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
			result, getErr := dsClient.Get(context.TODO(), daemonset.Name, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("failed to get latest version of Deployment: %v", getErr)
			}
			result.Spec = daemonset.Spec
			_, updateErr := dsClient.Update(context.TODO(), result, metav1.UpdateOptions{})
			return updateErr
		})
		if retryErr != nil {
			return fmt.Errorf("update failed for %v: %v", dsName, retryErr)
		}
		klog.Infof("Updated daemonset: %v", dsName)

	}
	return nil
}

// TODO : Replace these daemonsets with RuntimeClass with overhead. Perhaps 500m per build.
func systemReservingDaemonset(ciWorkloadName string, cpuQuantity string, memQuantity string) *appsv1.DaemonSet {

	workloadTaintName := CiWorkloadTestsTaintName
	if ciWorkloadName == CiWorkloadLabelValueBuilds {
		workloadTaintName = CiWorkloadBuildsTaintName
	}

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-system-reserving-daemonset-" + ciWorkloadName,
			Namespace: DeploymentNamespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ci-system-reserving-daemonset": ciWorkloadName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"ci-system-reserving-daemonset": ciWorkloadName,
					},
				},
				Spec: corev1.PodSpec{
					// I don't think daemonset pods can be evicted but sdn pods set a priority, so follow
					// suite and try to protect this extra CPU and memory as best we can.
					PriorityClassName: "system-cluster-critical",



					// Toleration plus affinity will ensure this daemonset only runs
					// on nodes with ci-workload label set equal ciWorkloadName.
					Tolerations: []corev1.Toleration {
						{
							Key:               workloadTaintName,
							Operator:          "Exists",
							Effect:            "NoSchedule",
						},
						// Make the pods fairly hard to during normal turmoil.
						{
							Key:               "node.kubernetes.io/memory-pressure",
							Operator:          "Exists",
							Effect:            "NoSchedule",
						},
						{
							Key:               "node.kubernetes.io/not-ready",
							Operator:          "Exists",
							Effect:            "NoExecute",
						},
						{
							Key:               "node.kubernetes.io/unreachable",
							Operator:          "Exists",
							Effect:            "NoExecute",
						},
						{
							Key:               "node.kubernetes.io/disk-pressure",
							Operator:          "Exists",
							Effect:            "NoSchedule",
						},
						{
							Key:               "node.kubernetes.io/network-unavailable",
							Operator:          "Exists",
							Effect:            "NoSchedule",
						},
					},
					
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{ 
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      CiWorkloadLabelName,
												Operator: "In",
												Values:   []string{ciWorkloadName},
											},
										},
									},
								},
							},
						},
					},

					// Just run pause -- allowing container runtime / other system process to consume our requested memory
					Containers: []corev1.Container{
						{
							Name:  "pause",
							Image: "gcr.io/google_containers/pause:latest",
							Resources: corev1.ResourceRequirements {
								Limits: corev1.ResourceList {
									corev1.ResourceCPU: resource.MustParse(cpuQuantity),
									corev1.ResourceMemory: resource.MustParse(memQuantity),
								},
								Requests: corev1.ResourceList {
									corev1.ResourceCPU: resource.MustParse(cpuQuantity),
									corev1.ResourceMemory: resource.MustParse(memQuantity),
								},
							},
						},
					},
				},
			},
		},
	}
	return ds
}

func int32Ptr(i int32) *int32 { return &i }

func kubeletReservingDeployment(nodeName string, ciWorkloadName string, cpuQuantity string, memQuantity string) *appsv1.Deployment {
	workloadTaintName := CiWorkloadTestsTaintName
	if ciWorkloadName == CiWorkloadLabelValueBuilds {
		workloadTaintName = CiWorkloadBuildsTaintName
	}

	labelName := "cpu-reserve-" + nodeName
	deploymentName := labelName + "-" + ciWorkloadName

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: deploymentName,
			Namespace: DeploymentNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(2),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelName: ciWorkloadName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelName: ciWorkloadName,
					},
				},
				Spec: corev1.PodSpec{
					// Just run pause -- allowing other pods to consume our overhead when we are evicted
					Containers: []corev1.Container{
						{
							Name:  "pause",
							Image: "gcr.io/google_containers/pause:latest",
							Resources: corev1.ResourceRequirements {
								Limits: corev1.ResourceList {
									corev1.ResourceCPU: resource.MustParse(cpuQuantity),
									corev1.ResourceMemory: resource.MustParse(memQuantity),
								},
								Requests: corev1.ResourceList {
									corev1.ResourceCPU: resource.MustParse(cpuQuantity),
									corev1.ResourceMemory: resource.MustParse(memQuantity),
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Key:      workloadTaintName,
							Operator: "Exists",
							Effect:   "NoSchedule",
						},
					},
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm {
								{
									Weight:          1,
									PodAffinityTerm: corev1.PodAffinityTerm {
										LabelSelector: &metav1.LabelSelector {
											MatchLabels: map[string]string {
												labelName: ciWorkloadName,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return deployment
}
