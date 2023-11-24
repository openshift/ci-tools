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
	oc --context app.ci --namespace ci "$@"
}

os::log::info "Extracting production data we need to run check-gh-automation..."
OC extract secret/openshift-prow-github-app --keys appid,cert --to "${data}"

app_id=$(cat "${data}/appid")

os::log::info "Running check-gh-automation"
go run ./cmd/check-gh-automation --repo="$1" --bot=openshift-merge-robot --bot=openshift-ci-robot --github-app-id="$app_id" --github-app-private-key-path="${data}/cert"
