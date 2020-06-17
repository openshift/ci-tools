#!/usr/bin/env bash

set -euo pipefail

echo "Running golangci-lint"
# CI has HOME set to '/' causing the linter to try and create a cache at /.cache for which
# it doesn't have permissions.
if [[ $HOME = '/' ]]; then
  export HOME=/tmp
fi

# The thing has a -skip-{dirs,files} directive which is ignored by half the linters. Why is life so hard.
targets="$(find . -maxdepth 1 -type d|egrep -v 'git|_output|hack|vendor|^\.$'|sed -E 's/(.*)/\1\/\.\.\./g'|tr '\n' ' ')"
golangci-lint run
