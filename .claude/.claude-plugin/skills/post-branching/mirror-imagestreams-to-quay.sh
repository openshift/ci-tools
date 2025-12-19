#!/bin/bash
set -euo pipefail

[ "$#" -ne 2 ] && echo "Usage: $0 <new-version> <release-repo>" && exit 1

NEW_VERSION="$1"
RELEASE_REPO="$2"

[ ! -f "$RELEASE_REPO/hack/mirror_integration_stream.sh" ] && echo "Error: Release repo not found" && exit 1

cd "$RELEASE_REPO"

STREAMS=(
    "origin:${NEW_VERSION}"
    "ocp:${NEW_VERSION}"
    "ocp-private:${NEW_VERSION}-priv"
    "origin:sriov-${NEW_VERSION}"
    "origin:scos-${NEW_VERSION}"
    "origin:ptp-${NEW_VERSION}"
    "origin:metallb-${NEW_VERSION}"
)

MAX_RETRIES=3

mirror_stream() {
    local namespace="$1"
    local name="$2"
    local retries=0

    while [ $retries -lt $MAX_RETRIES ]; do
        if DRY_RUN=false IMAGESTREAM_NAMESPACE="$namespace" IMAGESTREAM_NAME="$name" ./hack/mirror_integration_stream.sh; then
            return 0
        fi
        retries=$((retries + 1))
        [ $retries -lt $MAX_RETRIES ] && echo "Retry $retries/$((MAX_RETRIES - 1)) for $namespace/$name..." && sleep 5
    done
    return 1
}

FAILED=0

for stream in "${STREAMS[@]}"; do
    NAMESPACE="${stream%%:*}"
    NAME="${stream##*:}"

    echo "Mirroring $NAMESPACE/$NAME..."

    if mirror_stream "$NAMESPACE" "$NAME"; then
        echo "✓ $NAMESPACE/$NAME"
    else
        echo "✗ $NAMESPACE/$NAME failed after $MAX_RETRIES attempts"
        FAILED=1
    fi
done

[ $FAILED -eq 1 ] && exit 1
echo "✓ All image streams mirrored to Quay"
