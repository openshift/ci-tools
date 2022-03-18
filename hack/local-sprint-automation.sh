#!/bin/bash

source "$(dirname "${BASH_SOURCE}")/lib/init.sh"

function cleanup() {
  return_code=$?
  os::cleanup::all
  os::util::describe_return_code "${return_code}"
  exit "${return_code}"
}
trap "cleanup" EXIT

if [[ -z "${RELEASE_REPO_DIR:-}" ]]; then
  os::log::fatal "\$RELEASE_REPO_DIR is required"
fi

data="${BASETMPDIR}/data"
mkdir -p "${data}"
mkdir -p "${data}/jira"
mkdir -p "${data}/pd"

function OC() {
	oc --context app.ci --namespace ci --as system:admin "$@"
}

os::log::info "Extracting production data we need to run sprint-automation..."
OC extract secret/slack-credentials-dptp-bot-alpha --keys oauth_token --to "${data}"
OC extract secret/jira-token-dptp-bot --keys token --to "${data}/jira"
OC extract secret/pagerduty --keys token --to "${data}/pd"

os::log::info "Running sprint-automation"
go run ./cmd/sprint-automation --week-start=true --slack-token-path "${data}/oauth_token" --pager-duty-token-file="${data}/pd/token" --jira-bearer-token-file="${data}/jira/token" --jira-endpoint=https://issues.redhat.com --log-level=trace
