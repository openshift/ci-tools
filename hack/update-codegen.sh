#!/usr/bin/env bash

set -euxo pipefail

cd pkg/lenses/stepgraph
go run ../../../vendor/github.com/go-bindata/go-bindata/v3/go-bindata/ -pkg=stepgraph -fs=false -nometadata=true static
cd -
gofmt -s -w pkg/lenses/stepgraph/bindata.go
sed -i 's/%v/%w/g' pkg/lenses/stepgraph/bindata.go

go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen crd:preserveUnknownFields=false object \
  paths=./pkg/api/testimagestreamtagimport/v1 \
  output:dir=./pkg/api/testimagestreamtagimport/v1
