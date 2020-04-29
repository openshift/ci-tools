#!/usr/bin/env bash

set -euo pipefail

echo "Checking gofmt"
gofmt -s -l $(go list -f '{{ .Dir }}' ./... ) | grep ".*\.go"; if [ "$$?" = "0" ]; then gofmt -s -d $(shell go list -f '{{ .Dir }}' ./... ); exit 1; fi

echo "Running go vet"
go vet ./...

echo "Running golangci-lint"
golangci-lint run --disable-all --enable=unused,deadcode,gosimple ./...
