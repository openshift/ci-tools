#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

echo "this is going to fail"
exit 1