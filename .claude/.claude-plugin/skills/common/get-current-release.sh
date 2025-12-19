#!/bin/bash
set -euo pipefail

RELEASE_REPO="${1:-../release}"
INFRA_PERIODICS="$RELEASE_REPO/ci-operator/jobs/infra-periodics.yaml"

[ ! -f "$INFRA_PERIODICS" ] && echo "Error: infra-periodics.yaml not found at $INFRA_PERIODICS" && exit 1

VERSION=$(grep -A 20 "name: periodic-prow-auto-config-brancher" "$INFRA_PERIODICS" | grep "current-release=" | head -1 | sed 's/.*--current-release=//' | awk '{print $1}')

[ -z "$VERSION" ] && echo "Error: Failed to extract current-release from periodic-prow-auto-config-brancher" && exit 1

NIGHTLY_API="https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/${VERSION}.0-0.nightly/tags"

ACCEPTED_COUNT=$(curl -s "$NIGHTLY_API" | jq '[.tags[] | select(.phase == "Accepted")] | length')

[ -z "$ACCEPTED_COUNT" ] && echo "Error: Failed to fetch nightly releases for $VERSION" && exit 1

if [ "$ACCEPTED_COUNT" -lt 3 ]; then
    echo "✗ Branching cannot proceed!"
    echo "✗ Target version: $VERSION"
    echo "✗ Accepted nightlies: $ACCEPTED_COUNT/3 required"
    echo "✗ Wait for at least 3 accepted nightlies in the $VERSION.0-0.nightly stream"
    echo "✗ Check: $NIGHTLY_API"
    exit 1
fi

echo "✓ Ready to branch: $VERSION"
echo "✓ Accepted nightlies: $ACCEPTED_COUNT/3 required"
echo "$VERSION"
