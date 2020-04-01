#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/lib/init.sh"

function cleanup() {
    return_code=$?
    os::util::describe_return_code "${return_code}"
    exit "${return_code}"
}
trap "cleanup" EXIT

bad_files=$(os::util::list_go_src_files | xargs gofmt -s -l)
if [[ -n "${bad_files}" ]]; then
    os::log::warning "The following files are incorrectly formatted:"
    echo "${bad_files}"
    os::log::fatal "Update formatting with \`make format\`"
fi
