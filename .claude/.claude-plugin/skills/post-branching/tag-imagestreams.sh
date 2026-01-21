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

for tag in $(oc --context=app.ci get is "$OLD_IS" -n "$NAMESPACE" -o json | jq -r '.status.tags[].tag'); do
    echo "Seeding ${NAMESPACE}/${NEW_IS}:$tag"
    oc --context=app.ci tag \
        --as system:admin \
        "${NAMESPACE}/${OLD_IS}:$tag" \
        "${NAMESPACE}/${NEW_IS}:$tag"
done

echo "✓ Tagged ${NAMESPACE}/${NEW_IS}"
