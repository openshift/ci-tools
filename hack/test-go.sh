#!/usr/bin/env bash

set -euo pipefail

JUNIT_ARG=""
if [[ -n ${ARTIFACT_DIR:-} ]]; then
  JUNIT_ARG="--junitfile=$ARTIFACT_DIR/junit.xml"
fi

set -o xtrace
gotestsum $JUNIT_ARG -- ${PACKAGES:-"./..."} -race ${TESTFLAGS:-}
