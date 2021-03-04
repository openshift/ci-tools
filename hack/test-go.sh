#!/usr/bin/env bash

set -euo pipefail

JUNIT_ARG=""
if [[ -n ${ARTIFACT_DIR:-} ]]; then
  JUNIT_ARG="--junitfile=$ARTIFACT_DIR/junit.xml"
fi

# We embedd this so it must exist for compilation to succeed, but it's not checked in
if [[ -n ${CI:-} ]]; then touch cmd/vault-secret-collection-manager/index.js; fi

set -o xtrace
gotestsum $JUNIT_ARG -- ${PACKAGES:-"./..."} -race ${TESTFLAGS:-}
