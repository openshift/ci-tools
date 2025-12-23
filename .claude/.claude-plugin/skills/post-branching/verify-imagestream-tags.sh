#!/bin/bash
set -euo pipefail

[ "$#" -lt 3 ] && echo "Usage: $0 <old-version> <new-version> <namespace> [is-prefix] [is-suffix]" && exit 1

OLD_VERSION="$1"
NEW_VERSION="$2"
NAMESPACE="$3"
IS_PREFIX="${4:-}"
IS_SUFFIX="${5:-}"

OLD_IS="${IS_PREFIX}${OLD_VERSION}${IS_SUFFIX}"
NEW_IS="${IS_PREFIX}${NEW_VERSION}${IS_SUFFIX}"

OLD_COUNT=$(oc --context=app.ci get is "$OLD_IS" -n "$NAMESPACE" -o json 2>/dev/null | jq '.status.tags | length' || echo "0")
NEW_COUNT=$(oc --context=app.ci get is "$NEW_IS" -n "$NAMESPACE" -o json 2>/dev/null | jq '.status.tags | length' || echo "0")

echo "$NAMESPACE/$OLD_IS: $OLD_COUNT tags"
echo "$NAMESPACE/$NEW_IS: $NEW_COUNT tags"

if [ "$OLD_COUNT" -eq "$NEW_COUNT" ] && [ "$NEW_COUNT" -gt 0 ]; then
    echo "✓ Tag counts match"
    exit 0
else
    echo "✗ Tag count mismatch or zero tags"
    exit 1
fi
