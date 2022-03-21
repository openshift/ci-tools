#!/bin/bash

# This script connects to the placeholder service in the app.ci
# cluster for the alpha version of the Slack bot, connecting it
# to a local instance of the bot code running from the checked-
# out code. This is necessary to allow for properly running the
# MITM proxy we use to capture traffic into and out of the bot.

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

function OC() {
	oc --context app.ci --namespace ci --as system:admin "$@"
}

os::log::info "Extracting production data we need to run the server..."
OC extract secret/slack-credentials-dptp-bot-alpha --keys oauth_token,signing_secret --to "${data}"
OC extract secret/jira-token-dptp-bot --keys token --to "${data}"

os::log::info "Setting up the regular proxy for outgoing traffic..."
mitmdump --listen-port 7777 --mode regular --save-stream-file "${data}/regular.txt" &

os::log::info "Waiting for the regular proxy to start..."
while true; do
	if http_proxy=localhost:7777 https_proxy=localhost:7777 curl mitm.it >&/dev/null 2>&1; then
		break
	fi
	sleep 1
done

os::log::info "Installing the proxy's certificate..."
http_proxy=localhost:7777 https_proxy=localhost:7777 wget --output-document "${data}/mitm.pem" mitm.it/cert/pem

if [ "$(uname -s)" != "Darwin" ]; then
  sudo cp "${data}/mitm.pem" /etc/pki/ca-trust/source/anchors/
  sudo update-ca-trust
else # special logic for MacOS
  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "${data}/mitm.pem"
fi

os::log::info "Setting up the reverse proxy for incoming traffic"
mitmdump --listen-port 8888 --mode reverse:http://127.0.0.1:6666 --save-stream-file "${data}/reverse.txt" &

os::log::info "Setting up a connection to the Pod..."
OC port-forward deployment/slack-bot-alpha 2222:2222 &
sleep 2
os::log::info "Sending production traffic from Slack to the reverse proxy..."
ssh -N -T root@127.0.0.1 -p 2222 -R "8888:127.0.0.1:8888" &

os::log::info "Running the slack-bot server..."
http_proxy=localhost:7777 https_proxy=localhost:7777 slack-bot --port 6666 --slack-token-path "${data}/oauth_token" --slack-signing-secret-path="${data}/signing_secret" --jira-bearer-token-file="${data}/token" --jira-endpoint https://issues.redhat.com --log-level=trace --prow-config-path="${RELEASE_REPO_DIR}/core-services/prow/02_config/_config.yaml" --prow-job-config-path="${RELEASE_REPO_DIR}/ci-operator/jobs"
