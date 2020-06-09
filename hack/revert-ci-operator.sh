#!/usr/bin/env bash

set -euo pipefail


OLD_LATEST="$(oc --context api.ci get is pj-rehearse -n ci -o jsonpath={.status.tags[?\(@.tag==\"latest\"\)].items[1].dockerImageReference}|cut -d '@' -f2)"

echo "execute \`oc --context api.ci tag ci/pj-rehearse@$OLD_LATEST ci/pj-rehearse:latest\`"
