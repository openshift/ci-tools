#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

pull="${1:-"none"}"
if [[ "${pull}" == "none" ]]; then
	echo "[ERROR] Pull request not specified. Usage:"
	echo "[ERROR] $0 <pull-number>"
	exit 1
fi

jobs=( $( oc --context app.ci --namespace ci get prowjobs --selector prow.k8s.io/job=pull-ci-openshift-ci-tools-master-e2e,prow.k8s.io/refs.pull="${pull}" -o 'jsonpath={.items[?(@.status.state=="pending")].metadata.name}' ) )
if [[ "${#jobs[@]}" -lt 1 ]]; then
	echo "[INFO] No running e2e jobs found for pull ${pull}"
	exit 0
fi

echo "[INFO] Found ${#jobs[@]} running e2e jobs for pull ${pull}..."
for job in "${jobs[@]}"; do
	json="$( oc --context app.ci --namespace ci get prowjob "${job}" -o json )"
	cluster="$( jq .spec.cluster --raw-output <<<"${json}" )"
	echo "[INFO] Debugging job ${job}: build ID $( jq .status.build_id --raw-output <<<"${json}" ) on cluster ${cluster}"
	echo "[INFO] Waiting for top-level ci-operator Pod to start running..."
	while ! oc --context "${cluster}" --namespace ci logs "${job}" -c test >/dev/null 2>&1; do
		echo -n '.'
		sleep 5
	done
	echo
	namespace="$( oc --context "${cluster}" --namespace ci logs "${job}" -c test | grep -Po "(?<=projects/).*(?=$)" )"
	echo "[INFO] Waiting for e2e Pod to start running in namespace ${namespace} on cluster ${cluster}..."
	while ! oc --context "${cluster}" --namespace "${namespace}" get pod e2e-e2e >/dev/null 2>&1; do
		echo -n '.'
		sleep 30
	done
	echo
	log="$( oc --context "${cluster}" --namespace "${namespace}" exec e2e-e2e -- cat /tmp/cmd/tmp_stderr.log )"
	sub_namespace="$( grep -Po "(?<=projects/).*(?=$)" <<<"${log}" )"
	echo "[INFO] Current e2e test is running in namespace ${sub_namespace} on cluster ${cluster}..."
	echo
	echo "[INFO] Current test namespace state:"
	oc --context "${cluster}" --namespace "${sub_namespace}" get pods
	echo
	echo "[INFO] Current test output:"
	echo "${log}"
	echo
	echo "[INFO] For current test output, run:"
	echo "oc --context ${cluster} --namespace ${namespace} exec e2e-e2e -- cat /tmp/cmd/tmp_stderr.log"
	echo "[INFO] For test namespace state, run:"
	echo "oc --context ${cluster} --namespace ${sub_namespace} get pods"
done