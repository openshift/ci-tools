#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

dir=${BASETMPDIR}
export SHARED_DIR=${dir}/shared
export TMPDIR=${dir}/tmp
export CLI_DIR="/cli-dir"
export NAMESPACE=test
export JOB_NAME_SAFE=test
OUT=${dir}/out.log
ERR=${dir}/err.log
SECRET=${dir}/secret.yaml

fail() {
    echo -n "$1"
    if [[ -e "${ERR}" ]]; then
        echo ', output:'
        cat "${ERR}"
    else
        echo
    fi
    return 1
}

setup_test() {
    mkdir -p "${SHARED_DIR}"
    echo test0 > "${SHARED_DIR}/test0.txt"
    printf %s > "${SECRET}" '{' \
        '"kind":"Secret",' \
        '"apiVersion":"v1",' \
        '"metadata":{' \
            '"name":"test",' \
            '"creationTimestamp":null,' \
            '"labels":{' \
                '"ci.openshift.io/skip-censoring":"true"' \
            '}' \
        '},' \
        '"data":{' \
            '"test0.txt":"dGVzdDAK"' \
        '},' \
        '"type":"Opaque"' \
    $'}\n'
}

cleanup_test() { rm -rf "${dir}"; }

run_test() {
    cleanup_test
    setup_test
    eval "$@"
}

test_mkdir() {
    echo '[INFO] Verifying the directory is created'
    [[ ! -e "${TMPDIR}" ]]
    if ! entrypoint-wrapper --dry-run true > /dev/null 2> "${ERR}"; then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    if ! [[ -e "${TMPDIR}" ]]; then
        fail '[ERROR] entrypoint-wrapper did not create the directory'
    fi
}

test_shared_dir() {
    echo '[INFO] Verifying SHARED_DIR is set correctly'
    if ! v=$(entrypoint-wrapper --dry-run bash -c 'echo >&3 "${SHARED_DIR}"' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    diff <(echo "$v") <(cd "${TMPDIR}"/secret && pwd)
}

test_cli_dir() {
    echo '[INFO] Verifying PATH is set correctly when CLI_DIR is set'
    if ! v=$(entrypoint-wrapper --dry-run bash -c 'echo >&3 "${PATH}"' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    diff <(echo "$v") <(echo "${PATH}:${CLI_DIR}")
}

test_home_dir() {
    echo '[INFO] Verifying HOME is set correctly when original is not set'
    if ! v=$(unset HOME; entrypoint-wrapper --dry-run bash -c 'echo >&3 "${HOME}"' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    diff <(echo "$v") <(echo "/alabama")

    echo '[INFO] Verifying HOME is set correctly when original is not writeable'
    if ! v=$(HOME=nowhere entrypoint-wrapper --dry-run bash -c 'echo >&3 "${HOME}"' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    diff <(echo "$v") <(echo "/alabama")

    echo '[INFO] Verifying that setting HOME does not change the rest of the env'
    if ! v=$(unset HOME; WHOA=yes entrypoint-wrapper --dry-run bash -c 'echo >&3 "${WHOA}"' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    diff <(echo "$v") <(echo "yes")

    echo '[INFO] Verifying HOME is untouched when original is writeable'
    if ! v=$(HOME=/tmp entrypoint-wrapper --dry-run bash -c 'echo >&3 "${HOME}"' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    diff <(echo "$v") <(echo "/tmp")
}

test_copy_kubeconfig() {
    echo '[INFO] Verifying KUBECONFIG is not set when original is not set'
    if ! v=$(unset KUBECONFIG; entrypoint-wrapper --dry-run bash -c 'echo >&3 "${KUBECONFIG}"' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    diff <(echo "$v") <(echo "")

    echo '[INFO] Verifying KUBECONFIG is set correctly when original is set'
    if ! v=$(KUBECONFIG=a entrypoint-wrapper --dry-run bash -c 'echo >&3 "${KUBECONFIG}"' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    if [[ "${v}" == "a" ]]; then
      echo "\$KUBECONFIG was not changed!"
      return 1
    fi

    echo '[INFO] Verifying that setting HOME does not change the rest of the env'
    if ! v=$(KUBECONFIG=a WHOA=yes entrypoint-wrapper --dry-run bash -c 'echo >&3 "${WHOA}"' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    diff <(echo "$v") <(echo "yes")

    echo '[INFO] Verifying KUBECONFIG is populated when possible'
    echo "test" > "${dir}/kubeconfig.new"
    ( sleep 1 && mv "${dir}/kubeconfig.new" "${dir}/kubeconfig" ) &
    if ! v=$( \
        KUBECONFIG="${dir}/kubeconfig" \
        entrypoint-wrapper --dry-run \
            bash -c 'for i in {0..9}; do if [[ -f "${KUBECONFIG}" ]]; then cat "${KUBECONFIG}" >&3; break; fi; sleep 0.2; done' \
        3>&1 > /dev/null 2> "${ERR}")
    then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    diff <(echo "$v") <(echo "test")
    wait
}

test_copy_dir() {
    local data_dir=..2020_03_09_17_18_45.291041453
    echo '[INFO] Verifying SHARED_DIR is copied correctly'
    mkdir "${SHARED_DIR}/${data_dir}"
    echo test0 > "${SHARED_DIR}/${data_dir}/test0.txt"
    ln -s "${data_dir}" "${SHARED_DIR}/..data"
    ln -s ..2020_03_09_17_18_45.291041453 "${SHARED_DIR}/..data"
    ln -s ..data/test0.txt "${SHARED_DIR}/test0.txt"
    [[ ! -e "${TMPDIR}/secret/test.txt" ]]
    if ! entrypoint-wrapper --dry-run true > /dev/null 2> "${ERR}"; then
        fail '[ERROR] entrypoint-wrapper failed'
    fi
    echo test0 | diff "${TMPDIR}/secret/test0.txt" -
    if [[ -L "${TMPDIR}/secret/..data" ]]; then
        fail '[ERROR] symlinks should not be copied'
    fi
    if [[ -e "${TMPDIR}/secret/..2020_03_09_17_18_45.291041453" ]]; then
        fail '[ERROR] directories should not be copied'
    fi
}

test_signal() {
    local pid
    entrypoint-wrapper --dry-run sleep 1d > "${OUT}" 2> "${ERR}" &
    pid=$!
    if ! timeout 1s sh -c \
        'until pgrep -P "$1" sleep > /dev/null ; do :; done' \
        sh "${pid}"
    then
        kill "${pid}"
        fail '[ERROR] timeout while waiting for `sleep` to start:'
    fi
    kill -s "$1" "${pid}"
    if wait "$pid"; then
        fail "[ERROR] entrypoint-wrapper did not fail as expected:"
    elif ! cmp --quiet "${OUT}" "${SECRET}"; then
        echo '[ERROR] output:'
        cat "${OUT}"
        echo '[ERROR] error output:'
        cat "${ERR}"
    fi
}

os::test::junit::declare_suite_start "integration/entrypoint-wrapper"
os::cmd::expect_success 'run_test test_mkdir'
os::cmd::expect_success 'run_test test_shared_dir'
os::cmd::expect_success 'run_test test_cli_dir'
os::cmd::expect_success 'run_test test_copy_dir'
os::cmd::expect_success 'run_test test_home_dir'
os::cmd::expect_success 'run_test test_copy_kubeconfig'
os::cmd::expect_success "run_test entrypoint-wrapper --dry-run true \> ${OUT}"
os::integration::compare "${OUT}" "${SECRET}"
os::cmd::expect_failure "run_test entrypoint-wrapper --dry-run false \> ${OUT}"
os::integration::compare "${OUT}" "${SECRET}"
os::cmd::expect_success "run_test test_signal INT"
os::integration::compare "${OUT}" "${SECRET}"
os::cmd::expect_success "run_test test_signal TERM"
os::integration::compare "${OUT}" "${SECRET}"
os::test::junit::declare_suite_end
