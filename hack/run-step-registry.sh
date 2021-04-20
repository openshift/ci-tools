#!/bin/bash

set -euo pipefail

go install ./cmd/ci-operator-configresolver/
echo "UI is available on http://127.0.0.1:8082"
ci-operator-configresolver \
 -config=../release/ci-operator/config \
 -registry=../release/ci-operator/step-registry
