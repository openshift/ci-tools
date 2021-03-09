#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# This script checks to make sure that the vendored version of the test-infra repo here is
# no newer than those vendored into the release controller and chat bot. We assume a layout
# of repositories as we get from Prow's pod utilities.

function determine_vendored_commit() {
	local dir="$1"
	pushd "${dir}" >/dev/null
	local version
	# this will be of the form v0.0.0-20210115214543-aefe406fe7b6
	version=$( go list -mod=mod -m k8s.io/test-infra )
	local commit
	# this extracts aefe406fe7b6
	commit="${version##*-}"
	popd >/dev/null
	echo "${commit}"
}

ci_tools_vendored_commit="$( determine_vendored_commit . )"
release_controller_vendored_commit="$( determine_vendored_commit ./../release-controller )"

pushd ./../../kubernetes/test-infra
if ! git merge-base --is-ancestor "${ci_tools_vendored_commit}" "${release_controller_vendored_commit}"; then
	echo "[FATAL] The release-controller repo vendors test-infra at ${release_controller_vendored_commit}, which is older than the ci-tools vendor at ${ci_tools_vendored_commit}"
	exit 1
fi
popd
