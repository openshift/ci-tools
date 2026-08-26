#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/ci-operator-prowgen"
actual="${BASETMPDIR}/jobs"
mkdir -p "${actual}"
# we need to seed this with the input data as we operate "in place"
cp -a "${suite_dir}/input/jobs/." "${actual}"

os::test::junit::declare_suite_start "integration/ci-operator-prowgen"
# This test validates the ci-operator-prowgen tool

os::cmd::expect_success "ci-operator-prowgen --registry ${suite_dir}/input/registry --known-infra-file infra-periodics.yaml --from-dir ${suite_dir}/input/config --to-dir ${actual}"
os::integration::compare "${actual}" "${suite_dir}/output/jobs"

os::test::junit::declare_suite_end

os::test::junit::declare_suite_start "integration/ci-operator-prowgen-from-file"

from_file_actual="${BASETMPDIR}/from-file-jobs"
mkdir -p "${from_file_actual}"

# Run --from-file with two configs (one needing registry)
os::cmd::expect_success "ci-operator-prowgen --registry ${suite_dir}/input/registry --from-file ${suite_dir}/input/config/norehearsals/duper/norehearsals-duper-master.yaml --from-file ${suite_dir}/input/config/super/duper/super-duper-master.yaml --to-dir ${from_file_actual}"
os::integration::compare "${from_file_actual}" "${suite_dir}/output/from-file"

# No temp files should be left behind (atomic write cleanup)
os::cmd::expect_success_and_not_text "find ${from_file_actual} -name '.*.tmp-*' -type f" '.'

# Pre-existing files for other branches must be left untouched
other_branch_dir="${BASETMPDIR}/from-file-other-branch"
mkdir -p "${other_branch_dir}/super/duper"
other_branch_file="${other_branch_dir}/super/duper/super-duper-release-4.18-presubmits.yaml"
cat > "${other_branch_file}" <<'YAML'
presubmits:
  super/duper:
  - agent: kubernetes
    name: pull-ci-super-duper-release-4.18-unit
    labels:
      ci.openshift.io/generator: prowgen
YAML
cp "${other_branch_file}" "${other_branch_file}.before"

os::cmd::expect_success "ci-operator-prowgen --registry ${suite_dir}/input/registry --from-file ${suite_dir}/input/config/super/duper/super-duper-master.yaml --to-dir ${other_branch_dir}"
os::cmd::expect_success "diff ${other_branch_file} ${other_branch_file}.before"

os::test::junit::declare_suite_end

os::test::junit::declare_suite_start "integration/ci-operator-prowgen-managed-repos"

managed_actual="${BASETMPDIR}/managed-jobs"
mkdir -p "${managed_actual}"

# Seed a stale job file for the managed repo
managed_jobs_dir="${managed_actual}/super/duper"
mkdir -p "${managed_jobs_dir}"
stale_file="${managed_jobs_dir}/super-duper-master-presubmits.yaml"
cat > "${stale_file}" <<'YAML'
presubmits:
  super/duper:
  - agent: kubernetes
    name: pull-ci-super-duper-master-stale
    labels:
      ci.openshift.io/generator: prowgen
YAML
cp "${stale_file}" "${stale_file}.before"

# Create a managed repos config that manages super/duper
managed_config="${BASETMPDIR}/managed-repos.yaml"
cat > "${managed_config}" <<'YAML'
repos:
  super/duper:
    allBranches: true
YAML

# Run --from-dir with managed repos — super/duper should be skipped entirely
os::cmd::expect_success "ci-operator-prowgen --registry ${suite_dir}/input/registry --known-infra-file infra-periodics.yaml --managed-repos-config ${managed_config} --from-dir ${suite_dir}/input/config --to-dir ${managed_actual}"

# The stale file for the managed repo must be left untouched
os::cmd::expect_success "diff ${stale_file} ${stale_file}.before"

# Unmanaged repos should still get their jobs generated
os::cmd::expect_success "test -f ${managed_actual}/norehearsals/duper/norehearsals-duper-master-presubmits.yaml"

os::test::junit::declare_suite_end
