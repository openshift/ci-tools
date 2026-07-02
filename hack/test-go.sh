#!/usr/bin/env bash

set -euo pipefail

JUNIT_ARG=""
if [[ -n ${ARTIFACT_DIR:-} ]]; then
  JUNIT_ARG="--junitfile=$ARTIFACT_DIR/junit.xml"
fi

set -o xtrace
go run gotest.tools/gotestsum@latest $JUNIT_ARG -- ${PACKAGES:-"./..."} -race ${TESTFLAGS:-}
