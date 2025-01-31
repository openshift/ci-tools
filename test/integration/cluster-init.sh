#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
    rm -rf ${tempdir}
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/cluster-init/"
tempdir="${BASETMPDIR}/cluster-init"
mkdir -p "${tempdir}"
cp -a "${suite_dir}"/* "${tempdir}"

install_base="$tempdir/install-base"
mkdir -p "$install_base/ocp-install-base/auth"
kubeconfigs="$tempdir/kubeconfigs"
mkdir -p "$kubeconfigs"
clusterinstall_dir="$tempdir/clusterinstall"
mkdir -p "$clusterinstall_dir"

function generate_kubeconfig() {
    clustername="$1"
    path="$2"
    cat >"$path" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://api.${clustername}.fake:6443
  name: api-${clustername}-ci-devcluster-openshift-com
contexts:
- context:
    cluster: api-${clustername}-ci-devcluster-openshift-com
    namespace: ci
    user: user/api-${clustername}-ci-devcluster-openshift-com
  name: ${clustername}
name: ${clustername}
current-context: ${clustername}
preferences: {}
users:
- name: user/api-${clustername}-ci-devcluster-openshift-com
  user:
    token: xxx
EOF
}

generate_kubeconfig "build99" "$kubeconfigs/build99.config"

os::test::junit::declare_suite_start "integration/cluster-init"

# test the update ocp scenario
actual_update="${tempdir}/update-build99/input"
expected_update="${suite_dir}/update-build99/expected"
cat >"${clusterinstall_dir}/build99.yaml" <<EOF
clusterName: build99
credentialsMode: Manual
provision:
  aws: {}
onboard:
    hosted: false
    osd: false
    unmanaged: false
    useTokenFileInKubeconfig: true
    multiarch: true
    ciSchedulingWebhook:
      patches:
      - matches:
        - kind: MachineAutoscaler
          name: '^.+\-us-east-2b$'
        - kind: MachineAutoscaler
          name: '^.+\-us-east-2a$'
        inline: {"spec":{"minReplicas": 0}}
      - type: json-patch
        matches:
        - kind: MachineSet
          name: '^.+\-amd64-us-east-2a$'
        inline: [{"op": "add", "path": "/spec/template/spec/providerSpec/value/blockDevices/0/ebs/iops", "value": 0}]
    cloudCredential:
      aws: {}
EOF

export CITOOLS_CLUSTERINIT_INTEGRATIONTEST="1"
export CITOOLS_REPLAYTRANSPORT_MODE="read"
export CITOOLS_REPLAYTRANSPORT_TRACKER="${suite_dir}/build99-replay.yaml"

os::cmd::expect_success "cluster-init onboard config update --release-repo=$actual_update --cluster-install-dir=$clusterinstall_dir --kubeconfig-dir=$kubeconfigs --kubeconfig-suffix=config"
os::integration::compare "${actual_update}" "${expected_update}"

os::test::junit::declare_suite_end