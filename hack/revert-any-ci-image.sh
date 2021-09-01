#!/usr/bin/env bash

# Sets the `latest` tag of any imagestream in `ci` namespace to previous image
#
# Used to quickly revert broken images after a bad merge, to avoid waiting for
# lenghtly image builds after the bad code revert

set -euo pipefail

IMAGE=${1:-}

if [[ -z $IMAGE ]]; then
  echo "Sets the 'latest' tag of any imagestream in 'ci' namespace to previous image"
  echo "  (used to quickly revert broken images after a bad merge, to avoid waiting for lengthy image builds after the bad code revert)"
  echo ""
  echo "  Usage:   revert-any-ci-image.sh <IMAGE>"
  echo "  Example: revert-any-ci-image.sh ci-operator"
  exit 1
fi


OLD_LATEST="$(oc --context app.ci get is $1 -n ci -o jsonpath={.status.tags[?\(@.tag==\"latest\"\)].items[1].dockerImageReference}|cut -d '@' -f2)"

oc --context app.ci tag "ci/$1@$OLD_LATEST" "ci/$1:latest"
