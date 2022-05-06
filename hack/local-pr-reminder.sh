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
OC extract configmap/pr-reminder-config --keys config.yaml --to "${data}"
OC extract configmap/sync-rover-groups --keys mapping.yaml --to "${data}"

os::log::info "Running pr-reminder"
go run ./cmd/pr-reminder --config-path="${data}/config.yaml" --rover-groups-config-path="${data}/mapping.yaml" --slack-token-path="${data}/oauth_token"
