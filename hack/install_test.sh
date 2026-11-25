#!/bin/bash
# Regression test for hack/install.sh go_install_with_retry.
#
# Exercises the production script entry point with a controlled go shim to
# verify retry on the exact transient HTTP/2 error shape observed in CI
# (Prow bin-arm64 postsubmit), immediate failure on non-transient errors,
# error bound with original exit status preserved, and unchanged race-install
# path.
#
# Deterministic: no network, no real Go builds, no real sleeps. All temp files
# live under an isolated TMPDIR cleaned on exit. The working tree is not
# modified (the second arg is omitted so the remove-dummy branch is not taken;
# production-install passes "remove-dummy" but that only deletes a non-essential
# dummy file and does not affect retry behavior).
#
# Run:  bash hack/install_test.sh
# From: repository root (hack/lib/init.sh resolves it automatically)
#
# This test is standalone; it is not wired into make test (which runs Go unit
# tests) or any CI target. It can be added to a verify or pre-submit script
# when the team chooses to do so.

set -euo pipefail

test_dir=$(mktemp -d)
trap 'rm -rf "${test_dir}"' EXIT

# Isolate TMPDIR so init.sh helpers write inside our disposable tree.
export TMPDIR="${test_dir}/tmp"
mkdir -p "${TMPDIR}"

test_bin="${test_dir}/bin"
mkdir -p "${test_bin}"

# ---------------------------------------------------------------------------
# Shims: override sleep (no-op) and go (controlled, fail-closed)
# ---------------------------------------------------------------------------

printf '#!/bin/bash\nexit 0\n' > "${test_bin}/sleep"
chmod +x "${test_bin}/sleep"

cat > "${test_bin}/go" << 'GO_SHIM'
#!/bin/bash
# go shim: for "go install" calls, simulate success or failure based on
# TEST_MODE. Any unexpected subcommand (not "install") or unknown TEST_MODE
# fails immediately so the test does not accidentally use real Go.
if [[ "$1" != "install" ]]; then
    echo "go shim: unexpected subcommand '$1' (only 'install' is shimmed)" >&2
    exit 99
fi

count_file="${TEST_CALL_COUNT_FILE:?}"
count=$(( $(cat "$count_file" 2>/dev/null || echo 0) + 1 ))
echo "$count" > "$count_file"

case "${TEST_MODE:?}" in
    prow_fixture_then_success)
        # Verbatim Prow diagnostic from bin-arm64 postsubmit: note the
        # double-quoted URL with .zip" before the colon.
        if [[ $count -le 1 ]]; then
            echo '/go/pkg/mod/k8s.io/apimachinery@v0.37.1/pkg/util/net/http.go:39:2: golang.org/x/net@v0.62.0: read "https://proxy.golang.org/golang.org/x/net/@v/v0.62.0.zip": stream error: stream ID 123; INTERNAL_ERROR; received from peer' >&2
            exit 1
        fi
        exit 0
        ;;
    prow_fixture_permanent)
        # Same error shape, different module; use a distinct exit code (42)
        # to prove the original status is preserved through the retry loop.
        echo 'k8s.io/client-go@v0.37.1: read "https://proxy.golang.org/k8s.io/client-go/@v/v0.37.1.zip": stream error: stream ID 7; INTERNAL_ERROR; received from peer' >&2
        exit 42
        ;;
    compilation_error)
        # Non-transient error with a distinct exit code (7) to prove it
        # propagates without retry.
        echo "./cmd/test/main.go:10:3: undefined: bar" >&2
        exit 7
        ;;
    success)
        exit 0
        ;;
    *)
        echo "go shim: unknown TEST_MODE '${TEST_MODE}'" >&2
        exit 98
        ;;
esac
GO_SHIM
chmod +x "${test_bin}/go"

# ---------------------------------------------------------------------------
# Test infrastructure
# ---------------------------------------------------------------------------

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Skip all cmd/ directories except the first one found.
all_cmds=$(find "${REPO_ROOT}/cmd/" -mindepth 1 -maxdepth 1 -type d \
           -not \( -name '*ipi-deprovison*' \) -exec basename {} \; | sort)
test_cmd=$(echo "$all_cmds" | head -1)
skip_list=$(echo "$all_cmds" | grep -v "^${test_cmd}$" | paste -sd, -)

passed=0
failed=0
total=0

run_test() {
    local name="$1" mode="$2" script_args="$3"
    local expected_rc="$4" expected_calls="$5" expect_retry="$6"
    total=$((total + 1))

    echo "0" > "${test_dir}/calls"
    local output rc=0
    output=$(
        cd "${REPO_ROOT}"
        TEST_MODE="$mode" \
        TEST_CALL_COUNT_FILE="${test_dir}/calls" \
        SKIPPED_IMAGES="$skip_list" \
        PATH="${test_bin}:${PATH}" \
        bash hack/install.sh ${script_args} 2>&1
    ) || rc=$?

    local calls
    calls=$(cat "${test_dir}/calls")

    local ok=true detail=""

    # Verify exact exit code.
    if [[ $rc -ne $expected_rc ]]; then
        detail+=" rc=${rc}(want=${expected_rc})"
        ok=false
    fi

    # Verify call count.
    if [[ $calls -ne $expected_calls ]]; then
        detail+=" calls=${calls}(want=${expected_calls})"
        ok=false
    fi

    # Verify retry message presence (match runtime numbers, not source-code
    # templates that appear in the init.sh stack trace on error).
    local has_retry=false
    grep -qE 'on attempt [0-9]+/[0-9]+' <<< "$output" && has_retry=true
    if [[ "$expect_retry" == "yes" && "$has_retry" == "false" ]]; then
        detail+=" (missing retry message)"
        ok=false
    elif [[ "$expect_retry" == "no" && "$has_retry" == "true" ]]; then
        detail+=" (unexpected retry message)"
        ok=false
    fi

    if $ok; then
        echo "PASS  ${name} (rc=${rc} calls=${calls})"
        passed=$((passed + 1))
    else
        echo "FAIL  ${name}${detail}"
        failed=$((failed + 1))
    fi
}

# ---------------------------------------------------------------------------
# Test cases
# ---------------------------------------------------------------------------

echo "hack/install.sh go_install_with_retry regression tests"
echo "======================================================="

# Production-install path (no-race): retry enabled.
# The second arg is omitted (not "remove-dummy") so no source files are
# modified; this does not affect retry behavior.

run_test "prow fixture (zip stream INTERNAL_ERROR) retries and succeeds" \
    prow_fixture_then_success "no-race" \
    0 2 yes

run_test "persistent zip stream error exhausts 3 attempts, exit status 42 preserved" \
    prow_fixture_permanent "no-race" \
    42 3 yes

run_test "compilation error exit status 7 propagated without retry" \
    compilation_error "no-race" \
    7 1 no

run_test "immediate success passes through without retry" \
    success "no-race" \
    0 1 no

# Race-install path: retry disabled (RETRY_MODULE_ERRORS unset).
# The shim would succeed on attempt 2 but retry never happens, so the
# first attempt's exit 1 propagates.

run_test "race-install: matching error gets no retry" \
    prow_fixture_then_success "race" \
    1 1 no

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

echo ""
echo "Results: ${passed}/${total} passed"
if [[ $failed -ne 0 ]]; then
    echo "FAILED"
    exit 1
fi
echo "OK"
