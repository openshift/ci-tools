#!/usr/bin/env bash

set -euo pipefail

echo "Running golangci-lint"
# CI has HOME set to '/' causing the linter to try and create a cache at /.cache for which
# it doesn't have permissions.
if [[ $HOME = '/' ]]; then
  export HOME=/tmp
fi
golangci-lint run --disable-all --enable=unused,deadcode,gosimple,gofmt,govet ./...
