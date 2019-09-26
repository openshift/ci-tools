#!/bin/bash
set -euo pipefail

TMPDIR="$(mktemp -d)"
trap 'rm -r "${TMPDIR}"' EXIT
export TMPDIR
export NAMESPACE=test
export JOB_NAME_SAFE=test
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

test_output() {
    local out
    if ! out=$(diff /dev/fd/3 /dev/fd/4 3<<<"$1" 4<<<"$2"); then
        echo "${out}"
        echo '[ERROR] incorrect dry-run output'
        return 1
    fi
}

test_signal() {
    local pid
    secret-wrapper --dry-run sleep 1d &
    pid=$!
    if ! timeout 1s sh -c \
        'until pgrep --count --parent "$1" sleep > /dev/null ; do :; done' \
        sh "${pid}"
    then
        kill "${pid}"
        echo '[ERROR] timeout while waiting for `sleep` to start.'
        return 1
    fi
    kill -s "$1" "${pid}"
    if wait "$pid"; then
        echo "[ERROR] secret-wrapper did not fail as expected."
        return 1
    fi
}

mkdir "${TMPDIR}/secret"
echo test > "${TMPDIR}/secret/test.txt"

echo '[INFO] Running `secret-wrapper true`...'
if ! out=$(secret-wrapper --dry-run true); then
    echo "[ERROR] secret-wrapper failed."
    exit 1
fi
test_output "${out}" "${SECRET}"
echo '[INFO] Running `secret-wrapper false`...'
if out=$(secret-wrapper --dry-run false); then
    echo "[ERROR] secret-wrapper did not fail."
    exit 1
fi
test_output "${out}" ""
echo '[INFO] Running `secret-wrapper sleep 1d` and sending SIGINT...'
out=$(test_signal INT)
test_output "${out}" ""
echo '[INFO] Running `secret-wrapper sleep 1d` and sending SIGTERM...'
out=$(test_signal TERM)
test_output "${out}" ""
echo "[INFO] Success"
