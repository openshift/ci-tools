#!/bin/bash
set -euo pipefail

usage() {
    echo "Usage: $0 [--polling MINUTES] <job-name> [job-name...]"
    echo "  --polling MINUTES  Wait for job completion (timeout in minutes, default: no polling)"
    exit 1
}

[ "$#" -lt 1 ] && usage

POLLING=0
JOBS=()

while [ "$#" -gt 0 ]; do
    case "$1" in
        --polling)
            shift
            POLLING="$1"
            shift
            ;;
        *)
            JOBS+=("$1")
            shift
            ;;
    esac
done

[ ${#JOBS[@]} -eq 0 ] && usage

GANGWAY_URL="https://gangway-ci.apps.ci.l2s4.p1.openshiftapps.com"
TOKEN=$(oc --context app.ci whoami -t 2>/dev/null) || { echo "Error: Failed to get oc token"; exit 1; }

trigger_job() {
    local job_name="$1"
    local payload=$(cat <<EOF
{
  "job_execution_type": "1",
  "pod_spec_options": {
    "envs": {},
    "job_name": "$job_name"
  }
}
EOF
)

    curl -s -X POST \
        -H "Authorization: Bearer $TOKEN" \
        -H "Content-Type: application/json" \
        -d "$payload" \
        "$GANGWAY_URL/v1/executions" \
        | grep -o '"id":"[^"]*"' | cut -d'"' -f4
}

check_status() {
    curl -s -X GET \
        -H "Authorization: Bearer $TOKEN" \
        "$GANGWAY_URL/v1/executions/$1" \
        | grep -o '"job_status":"[^"]*"' | cut -d'"' -f4
}

wait_for_job() {
    local execution_id="$1"
    local job_name="$2"
    local max_wait=$((POLLING * 60))
    local elapsed=0

    while [ $elapsed -lt $max_wait ]; do
        local status=$(check_status "$execution_id")

        case "$status" in
            "success")
                echo "✓ $job_name completed"
                return 0
                ;;
            "failure"|"error"|"aborted")
                echo "✗ $job_name failed: $status"
                return 1
                ;;
            *)
                printf "."
                sleep 30
                elapsed=$((elapsed + 30))
                ;;
        esac
    done

    echo "✗ $job_name timeout after $POLLING minutes"
    return 1
}

FAILED=0
for job in "${JOBS[@]}"; do
    echo "Triggering $job..."
    execution_id=$(trigger_job "$job")

    if [ -z "$execution_id" ]; then
        echo "✗ Failed to trigger $job"
        FAILED=1
        continue
    fi

    echo "Execution ID: $execution_id"
    echo "View: https://prow.ci.openshift.org/?job=$job"

    if [ $POLLING -gt 0 ]; then
        wait_for_job "$execution_id" "$job" || FAILED=1
    fi
done

[ $FAILED -eq 1 ] && exit 1
[ $POLLING -gt 0 ] && echo "✓ All jobs completed"
[ $POLLING -eq 0 ] && echo "✓ All jobs triggered"
