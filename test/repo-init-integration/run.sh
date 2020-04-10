#!/bin/bash

# This test runs the repo-init utility and verifies that it generates
# correct CI Operator configs and edits Prow config as expected

set -o errexit
set -o nounset
set -o pipefail

ROOTDIR=$(pwd)
WORKDIR="$( mktemp -d )"
trap 'rm -rf ${WORKDIR}' EXIT

cd "$WORKDIR"
# copy config to tmpdir to allow tester to modify config
cp -ar "$ROOTDIR"/test/repo-init-integration/input/* .

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
               "no" # Are there any more end-to-end test scripts to configure?  [default: no] no
)
for input in "${inputs[@]}"; do echo "${input}"; done | repo-init -release-repo .

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
for input in "${inputs[@]}"; do echo "${input}"; done | repo-init -release-repo .
ci-operator-prowgen --from-dir ./ci-operator/config --to-dir ./ci-operator/jobs
sanitize-prow-jobs --prow-jobs-dir ./ci-operator/jobs --config-path ./core-services/sanitize-prow-jobs/_config.yaml

if [[  "${UPDATE:-}" = true ]]; then
  rm -rf  "$ROOTDIR"/test/repo-init-integration/expected/*
  cp -ar "$WORKDIR"/* "$ROOTDIR"/test/repo-init-integration/expected/
fi

if ! diff -Naupr "$ROOTDIR"/test/repo-init-integration/expected .> "$WORKDIR/diff"; then
    echo "[ERROR] Got incorrect output state after running repo-init:"
    cat "$WORKDIR/diff"
    echo "If this is expected, run \`make integration-repo-init-update\`"
    exit 1
fi
