#!/usr/bin/env bash

set -euo pipefail

cd $(dirname $0)/..

go run ./cmd/docgen
gofmt -s -w pkg/webreg/zz_generated.ci_operator_reference.go
