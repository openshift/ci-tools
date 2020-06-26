#!/bin/bash

# This library provides helper methods for use in the integration suites,
# in order to provide a consistent workflow and user experience.

function os::integration::generate() {
    :
}
readonly -f os::integration::generate

# os::integration::compare expects to find no difference between two inputs.
#
# If UPDATE=true is set, the function will first update the expected data
# with the generated data, then run the comparison.
function os::integration::compare() {
    local actual="$1"
    local expected="$2"

    if [[ "${UPDATE:-false}" == "true" ]]; then
        os::log::info "Updating golden files in ${expected}..."
        if [[ -d "${actual}" ]]; then
            cp -a "${actual}/." "${expected}"
        else
            cp "${actual}" "${expected}"
        fi
    fi

    os::cmd::expect_success "diff -Naupr ${actual} ${expected}"
}
readonly -f os::integration::compare

# os::integration::sanitize_prowjob_yaml replaces known variable fields in
# Kubernetes YAML with static strings in order to make comparisons easy.
function os::integration::sanitize_prowjob_yaml() {
    local data="$1"
    sed -i -E -e 's/sha: .+/sha: test_sha/g' -e 's/[a-z0-9]{8}-([a-z0-9]{4}-){3}[a-z0-9]{12}/test-prowjob/g' -e 's/startTime: .+/startTime: 2020-06-22T22:25:00Z/g' "${data}"
}
readonly -f os::integration::sanitize_prowjob_yaml

__os_integration_configresolver_pid=""

# os::integration::configresolver::start starts the configresolver
#
# Logs are saved under the $LOG_DIR for further processing.
function os::integration::configresolver::start() {
    local config="$1"
    local registry="$2"
    local prow="$3"
    local flat="${4:-}"

    os::util::ensure::gopath_binary_exists "ci-operator-configresolver"

    os::log::info "Starting the config resolver..."
    ci-operator-configresolver --config "${config}"       \
                               --registry "${registry}"   \
                               --prow-config "${prow}"    \
                               "${flat:+--flat-registry}" \
                               --log-level debug          \
                               --cycle 2m >"${LOG_DIR}/configresolver.log" 2>&1 &
    __os_integration_configresolver_pid="$!"
    os::integration::configresolver::wait_for_ready
}
readonly -f os::integration::configresolver::start

# os::integration::configresolver::stop stops the configresolver
function os::integration::configresolver::stop() {
    os::log::info "Stopping the config resolver..."
    if [[ -n "${__os_integration_configresolver_pid}" ]]; then
        ${USE_SUDO:+sudo} kill "${__os_integration_configresolver_pid}" &> /dev/null
    fi
}
readonly -f os::integration::configresolver::stop

# os::integration::configresolver::wait_for_ready polls until the config
# resolver is ready to serve content.
function os::integration::configresolver::wait_for_ready() {
    os::log::info "Waiting for the config resolver to be ready..."
    os::cmd::try_until_text "curl http://127.0.0.1:8081/healthz/ready" "OK"
}
readonly -f os::integration::configresolver::wait_for_ready

# os::integration::configresolver::generation::config gets the current config generation from the server.
function os::integration::configresolver::generation::config() {
    curl -s http://127.0.0.1:8080/configGeneration
}
readonly -f os::integration::configresolver::generation::config

# os::integration::configresolver::generation::registry gets the current registry generation from the server.
function os::integration::configresolver::generation::registry() {
    curl -s http://127.0.0.1:8080/registryGeneration
}
readonly -f os::integration::configresolver::generation::registry

# os::integration::configresolver::wait_for_config_update polls until the config
# resolver has updated to a newer generation than the provided one.
function os::integration::configresolver::wait_for_config_update() {
    local generation="$1"
    os::log::info "Waiting for the config resolver to update..."
    os::cmd::try_until_success "test \$( os::integration::configresolver::generation::config ) -gt ${generation}"
}
readonly -f os::integration::configresolver::wait_for_config_update

# os::integration::configresolver::wait_for_registry_update polls until the config
# resolver has updated to a newer generation than the provided one.
function os::integration::configresolver::wait_for_registry_update() {
    local generation="$1"
    os::log::info "Waiting for the config resolver to update..."
    os::cmd::try_until_success "test \$( os::integration::configresolver::generation::registry ) -gt ${generation}"
}
readonly -f os::integration::configresolver::wait_for_registry_update


# os::integration::configresolver::check_log searches for errors in the log.
function os::integration::configresolver::check_log() {
    if grep -qE "level=(error|fatal)" "${LOG_DIR}/configresolver.log"; then
        grep -E "level=(error|fatal)" "${LOG_DIR}/configresolver.log"
        os::log::fatal "Detected errors in the ci-operator-configresolver log!"
    fi
}
readonly -f os::integration::configresolver::check_log
