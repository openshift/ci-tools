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

pr_reminder_config=$(mktemp --tmpdir="${data}" pr-reminder-config.XXXXXXXXXX)
>${pr_reminder_config} cat <<EOF
  teams:
  - teamMembers:
    - ${USR:-sgoeddel}
    teamNames:
    - test-platform
    repos:
    - openshift/ci-tools
    - openshift/ci-docs
    - openshift/release
    - kubernetes/test-infra
EOF

function OC() {
	oc --context app.ci --namespace ci --as system:admin "$@"
}

os::log::info "Extracting production data we need to run pr-reminder..."
OC extract secret/slack-credentials-dptp-bot-alpha --keys oauth_token --to "${data}"
OC extract configmap/sync-rover-groups --keys users.yaml.tar.gz --to "${data}"
tar xvzf "${data}/users.yaml.tar.gz" -C "${data}/"

os::log::info "Running pr-reminder"
go run ./cmd/pr-reminder \
  --validate-only=false \
  --config-path="${pr_reminder_config}" \
  --github-users-file="${data}/users.yaml" \
  --slack-token-path="${data}/oauth_token"
