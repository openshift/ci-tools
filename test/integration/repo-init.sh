#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
    rm -rf ${tempdir}
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/repo-init/"
tempdir="${BASETMPDIR}/repo-init"
mkdir -p "${tempdir}"
cp -a "${suite_dir}"/* "${tempdir}"
actual="${tempdir}/input"
expected="${suite_dir}/expected"

os::test::junit::declare_suite_start "integration/repo-init"
# This test runs the repo-init utility and verifies that it generates
# correct CI Operator configs and edits Prow config as expected

# this test case will copy-cat origin
inputs=(
              "org" # Enter the organization for the repository: org
             "repo" # Enter the repository to initialize: repo
                 "" # Enter the development branch for the repository: [default: master]
              "yes" # Does the repository build and promote container images?  [default: no] yes
              "yes" # Does the repository promote images as part of the OpenShift release?  [default: no] yes
              "yes" # Do any images build on top of the OpenShift base image?  [default: no] yes
               "no" # Do any images build on top of the CentOS base image?  [default: no] no
                 "" # What version of Go does the repository build with? [default: 1.12]
                 "" # Enter the Go import path for the repository if it uses a vanity URL (e.g. "k8s.io/my-repo"):
     "make install" # What commands are used to build binaries in the repository? (e.g. "go install ./cmd/...") make install
"make test-install" # What commands, if any, are used to build test binaries? (e.g. "go install -race ./cmd/..." or "go test -c ./test/...") make test-install
              "yes" # Are there any test scripts to configure?  [default: no] yes
             "unit" # What is the name of this test (e.g. "unit")?  unit
               "no" # Does this test require built binaries?  [default: no] no
               "no" # Does this test require test binaries?  [default: no] no
             "unit" # What commands in the repository run the test (e.g. "make test-unit")?  make test-unit
              "yes" # Are there any more test scripts to configure?  [default: no] yes
              "cmd" # What is the name of this test (e.g. "unit")?  cmd
              "yes" # Does this test require built binaries?  [default: no] yes
    "make test-cmd" # What command  s in the repository run the test (e.g. "make test-unit")?  make test-cmd
              "yes" # Are there any more test scripts to configure?  [default: no] yes
             "race" # What is the name of this test (e.g. "unit")?  race
               "no" # Does this test require built binaries?  [default: no] no
              "yes" # Does this test require test binaries?  [default: no] yes
             "race" # What commands in the repository run the test (e.g. "make test-unit")?  make test-race
               "no" # Are there any more test scripts to configure?  [default: no] no
              "yes" # Are there any end-to-end test scripts to configure?  [default: no] yes
              "e2e" # What is the name of this test (e.g. "e2e-operator")?  e2e
                 "" # Which specific cloud provider does the test require, if any?  [default: aws]
              "e2e" # What commands in the repository run the test (e.g. "make test-e2e")?  make test-e2e
              "yes" # Does your test require the OpenShift client (oc)?
               "no" # Are there any more end-to-end test scripts to configure?  [default: no] no
)
export inputs
os::cmd::expect_success 'for input in "${inputs[@]}"; do echo "${input}"; done | repo-init -release-repo "${actual}"'

# this test case will copy-cat ci-tools
inputs=(
              "org" # Enter the organization for the repository: org
            "other" # Enter the repository to initialize: repo
      "nonstandard" # Enter the development branch for the repository: [default: master]
              "yes" # Does the repository build and promote container images?  [default: no] yes
                 "" # Does the repository promote images as part of the OpenShift release?  [default: no] yes
               "no" # Do any images build on top of the OpenShift base image?  [default: no] yes
               "no" # Do any images build on top of the CentOS base image?  [default: no] no
             "1.15" # What version of Go does the repository build with? [default: 1.12]
      "k8s.io/cool" # Enter the Go import path for the repository if it uses a vanity URL (e.g. "k8s.io/my-repo"):
                 "" # What commands are used to build binaries in the repository? (e.g. "go install ./cmd/...") make install
                 "" # What commands, if any, are used to build test binaries? (e.g. "go install -race ./cmd/..." or "go test -c ./test/...") make test-install
              "yes" # Are there any test scripts to configure?  [default: no] yes
             "unit" # What is the name of this test (e.g. "unit")?  unit
   "make test-unit" # What commands in the repository run the test (e.g. "make test-unit")?  make test-unit
               "no" # Are there any more test scripts to configure?  [default: no] yes
                 "" # Are there any end-to-end test scripts to configure?  [default: no] no
)
export inputs
os::cmd::expect_success 'for input in "${inputs[@]}"; do echo "${input}"; done | repo-init -release-repo "${actual}"'

# this test case will use a custom release
inputs=(
              "org" # Enter the organization for the repository: org
            "third" # Enter the repository to initialize: repo
      "nonstandard" # Enter the development branch for the repository: [default: master]
               "no" # Does the repository build and promote container images?  [default: no] yes
             "1.15" # What version of Go does the repository build with? [default: 1.12]
      "k8s.io/cool" # Enter the Go import path for the repository if it uses a vanity URL (e.g. "k8s.io/my-repo"):
                 "" # What commands are used to build binaries in the repository? (e.g. "go install ./cmd/...") make install
                 "" # What commands, if any, are used to build test binaries? (e.g. "go install -race ./cmd/..." or "go test -c ./test/...") make test-install
               "no" # Are there any test scripts to configure?  [default: no] yes
              "yes" # Are there any end-to-end test scripts to configure?  [default: no] yes
              "e2e" # What is the name of this test (e.g. "e2e-operator")?  e2e
                 "" # Which specific cloud provider does the test require, if any?  [default: aws]
              "e2e" # What commands in the repository run the test (e.g. "make test-e2e")?  make test-e2e
               "no" # Does your test require the OpenShift client (oc)? no
               "no" # Are there any more end-to-end test scripts to configure?  [default: no] no
          "nightly" # What type of OpenShift release do the end-to-end tests run on top of? [nightly, published]
              "4.4" # Which OpenShift version is being tested? [default: 4.6]
)
export inputs
os::cmd::expect_success 'for input in "${inputs[@]}"; do echo "${input}"; done | repo-init -release-repo "${actual}"'
os::cmd::expect_success 'ci-operator-prowgen --from-dir "${actual}/ci-operator/config" --to-dir "${actual}/ci-operator/jobs"'
os::cmd::expect_success 'sanitize-prow-jobs --prow-jobs-dir "${actual}/ci-operator/jobs" --config-path "${actual}/core-services/sanitize-prow-jobs/_config.yaml" --cluster-config-path "${actual}/core-services/sanitize-prow-jobs/_clusters.yaml"'
os::cmd::expect_success 'determinize-ci-operator --config-dir "${actual}/ci-operator/config" --confirm'
os::integration::compare "${actual}" "${expected}"

os::test::junit::declare_suite_end
