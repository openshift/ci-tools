package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestQueriesByMetric(t *testing.T) {
	expected := map[string]string{
		"pods/container_cpu_usage_seconds_total": `sum by (
    namespace,
    pod,
    container
  ) (rate(container_cpu_usage_seconds_total{container!="POD",container!=""}[3m]))
  * on(namespace,pod) 
  group_left(
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_openshift_io_build_name,
    label_ci_openshift_io_release,
    label_app
  ) max by (
    namespace,
    pod,
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_openshift_io_build_name,
    label_ci_openshift_io_release,
    label_app
  ) (kube_pod_labels{label_created_by_ci="true",label_ci_openshift_io_metadata_step=""})`,
		"pods/container_memory_working_set_bytes": `sum by (
    namespace,
    pod,
    container
  ) (container_memory_working_set_bytes{container!="POD",container!=""})
  * on(namespace,pod) 
  group_left(
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_openshift_io_build_name,
    label_ci_openshift_io_release,
    label_app
  ) max by (
    namespace,
    pod,
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_openshift_io_build_name,
    label_ci_openshift_io_release,
    label_app
  ) (kube_pod_labels{label_created_by_ci="true",label_ci_openshift_io_metadata_step=""})`,
		"prowjobs/container_cpu_usage_seconds_total": `sum by (
    namespace,
    pod,
    container
  ) (rate(container_cpu_usage_seconds_total{container!="POD",container!=""}[3m]))
  * on(namespace,pod) 
  group_left(
    label_created_by_prow,
    label_prow_k8s_io_context,
    label_prow_k8s_io_refs_org,
    label_prow_k8s_io_refs_repo,
    label_prow_k8s_io_refs_base_ref,
    label_prow_k8s_io_job,
    label_prow_k8s_io_type
  ) max by (
    namespace,
    pod,
    label_created_by_prow,
    label_prow_k8s_io_context,
    label_prow_k8s_io_refs_org,
    label_prow_k8s_io_refs_repo,
    label_prow_k8s_io_refs_base_ref,
    label_prow_k8s_io_job,
    label_prow_k8s_io_type
  ) (kube_pod_labels{label_created_by_prow="true",label_prow_k8s_io_job!="",label_ci_openshift_org_rehearse=""})`,
		"prowjobs/container_memory_working_set_bytes": `sum by (
    namespace,
    pod,
    container
  ) (container_memory_working_set_bytes{container!="POD",container!=""})
  * on(namespace,pod) 
  group_left(
    label_created_by_prow,
    label_prow_k8s_io_context,
    label_prow_k8s_io_refs_org,
    label_prow_k8s_io_refs_repo,
    label_prow_k8s_io_refs_base_ref,
    label_prow_k8s_io_job,
    label_prow_k8s_io_type
  ) max by (
    namespace,
    pod,
    label_created_by_prow,
    label_prow_k8s_io_context,
    label_prow_k8s_io_refs_org,
    label_prow_k8s_io_refs_repo,
    label_prow_k8s_io_refs_base_ref,
    label_prow_k8s_io_job,
    label_prow_k8s_io_type
  ) (kube_pod_labels{label_created_by_prow="true",label_prow_k8s_io_job!="",label_ci_openshift_org_rehearse=""})`,
		"steps/container_cpu_usage_seconds_total": `sum by (
    namespace,
    pod,
    container
  ) (rate(container_cpu_usage_seconds_total{container!="POD",container!=""}[3m]))
  * on(namespace,pod) 
  group_left(
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_ci_openshift_io_metadata_step
  ) max by (
    namespace,
    pod,
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_ci_openshift_io_metadata_step
  ) (kube_pod_labels{label_created_by_ci="true",label_ci_openshift_io_metadata_step!=""})`,
		"steps/container_memory_working_set_bytes": `sum by (
    namespace,
    pod,
    container
  ) (container_memory_working_set_bytes{container!="POD",container!=""})
  * on(namespace,pod) 
  group_left(
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_ci_openshift_io_metadata_step
  ) max by (
    namespace,
    pod,
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_ci_openshift_io_metadata_step
  ) (kube_pod_labels{label_created_by_ci="true",label_ci_openshift_io_metadata_step!=""})`,
	}
	if diff := cmp.Diff(expected, queriesByMetric()); diff != "" {
		t.Errorf("incorrect queries: %v", diff)
	}
}
