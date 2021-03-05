#!/usr/bin/env bash

set -euxo pipefail

go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen crd:preserveUnknownFields=false object \
  paths=./pkg/api/testimagestreamtagimport/v1 \
  output:dir=./pkg/api/testimagestreamtagimport/v1
