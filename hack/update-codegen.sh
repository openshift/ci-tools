#!/usr/bin/env bash

set -euxo pipefail

go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen object \
    paths=./pkg/api/testimagestreamtagimport/v1 \
    output:dir=./pkg/api/testimagestreamtagimport/v1

go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen crd:crdVersions=v1 object \
    paths=./pkg/api/pullrequestpayloadqualification/v1 \
    output:dir=./pkg/api/pullrequestpayloadqualification/v1

go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen object \
    paths=./pkg/api/ \
    output:dir=./pkg/api/

go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen crd:crdVersions=v1 object \
    paths=./pkg/api/multiarchbuildconfig/v1 \
    output:dir=./pkg/api/multiarchbuildconfig/v1
