#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

# This script checks to make sure that the vendored version of the kubernetes-sigs/prow repo here is
# no newer than those vendored into the release controller and chat bot. We assume a layout
# of repositories as we get from Prow's pod utilities.

function determine_vendored_commit() {
	local dir="$1"
	pushd "${dir}" >/dev/null
	local version
	# this will be of the form v0.0.0-20210115214543-aefe406fe7b6
	version=$( go list -mod=mod -m sigs.k8s.io/prow )
	local commit
	# this extracts aefe406fe7b6
	commit="${version##*-}"
	popd >/dev/null
	echo "${commit}"
}


ci_tools_vendored_commit="$( determine_vendored_commit . )"
release_controller_vendored_commit="$( determine_vendored_commit ./../release-controller )"
ci_chat_bot_vendored_commit="$( determine_vendored_commit ./../ci-chat-bot )"

failures=0

pushd ./../../kubernetes-sigs/prow
if ! git merge-base --is-ancestor "${ci_tools_vendored_commit}" "${release_controller_vendored_commit}"; then
	echo "[FATAL] The release-controller repo vendors prow at ${release_controller_vendored_commit}, which is older than the ci-tools vendor at ${ci_tools_vendored_commit}"
	failures=$((failures+1))
fi

if ! git merge-base --is-ancestor "${ci_tools_vendored_commit}" "${ci_chat_bot_vendored_commit}"; then
	echo "[FATAL] The ci-chat-bot repo vendors prow at ${ci_chat_bot_vendored_commit}, which is older than the ci-tools vendor at ${ci_tools_vendored_commit}"
	failures=$((failures+1))
fi
popd

exit $failures
