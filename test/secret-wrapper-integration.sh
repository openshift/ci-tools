#!/bin/bash
set -euo pipefail

TMPDIR="$(mktemp -d)"
trap 'rm -r "${TMPDIR}"' EXIT
export TMPDIR
export NAMESPACE=test
export JOB_NAME_SAFE=test
ERR=${TMPDIR}/err.log
SECRET='[
  {
    "kind": "Secret",
    "apiVersion": "v1",
    "metadata": {
      "name": "test",
      "creationTimestamp": null
    },
    "data": {
      "test.txt": "dGVzdAo="
    },
    "type": "Opaque"
  }
]'

fail() {
    echo "$1"
    cat "${ERR}"
    return 1
}

test_output() {
    local out
    if ! out=$(diff /dev/fd/3 /dev/fd/4 3<<<"$1" 4<<<"$2"); then
        echo '[ERROR] incorrect dry-run output:'
        echo "${out}"
        return 1
    fi
}

test_signal() {
    local pid
    secret-wrapper --dry-run sleep 1d 2> "${ERR}" &
    pid=$!
    if ! timeout 1s sh -c \
        'until pgrep --count --parent "$1" sleep > /dev/null ; do :; done' \
        sh "${pid}"
    then
        kill "${pid}"
        fail '[ERROR] timeout while waiting for `sleep` to start:'
    fi
    kill -s "$1" "${pid}"
    if wait "$pid"; then
        fail "[ERROR] secret-wrapper did not fail as expected:"
    fi
}

mkdir "${TMPDIR}/secret"
echo test > "${TMPDIR}/secret/test.txt"

echo '[INFO] Running `secret-wrapper true`...'
if ! out=$(secret-wrapper --dry-run true 2> "${ERR}"); then
    fail "[ERROR] secret-wrapper failed:"
fi
test_output "${out}" "${SECRET}"
echo '[INFO] Running `secret-wrapper false`...'
if out=$(secret-wrapper --dry-run false 2> "${ERR}"); then
    fail "[ERROR] secret-wrapper did not fail:"
fi
test_output "${out}" ""
echo '[INFO] Running `secret-wrapper sleep 1d` and sending SIGINT...'
out=$(test_signal INT)
test_output "${out}" ""
echo '[INFO] Running `secret-wrapper sleep 1d` and sending SIGTERM...'
out=$(test_signal TERM)
test_output "${out}" ""
echo "[INFO] Success"
