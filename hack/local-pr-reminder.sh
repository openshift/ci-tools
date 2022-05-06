#!/bin/bash

source "$(dirname "${BASH_SOURCE}")/lib/init.sh"

function cleanup() {
  return_code=$?
  os::cleanup::all
  os::util::describe_return_code "${return_code}"
  exit "${return_code}"
}
trap "cleanup" EXIT

data="${BASETMPDIR}/data"
mkdir -p "${data}"

function OC() {
	oc --context app.ci --namespace ci --as system:admin "$@"
}

os::log::info "Extracting production data we need to run pr-reminder..."
OC extract secret/slack-credentials-dptp-bot-alpha --keys oauth_token --to "${data}"

os::log::info "Running pr-reminder"
go run ./cmd/pr-reminder --config-path=./cmd/pr-reminder/testdata/config.yaml --rover-groups-config-path=./cmd/pr-reminder/testdata/rover-groups-config.yaml --slack-token-path="${data}/oauth_token"
