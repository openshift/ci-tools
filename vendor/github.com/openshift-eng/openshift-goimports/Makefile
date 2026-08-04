.DEFAULT_GOAL := help

verify: verify-gofmt ## Run verifications. Example: make verify
.PHONY: verify

verify-gofmt: ## Run gofmt verification. Example: make verify-gofmt
	scripts/verify-gofmt.sh
.PHONY: verify-gofmt

test: test-unit ## Run tests. Example: make test
.PHONY: test

test-unit: ## Run unit tests. Example: make test-unit
	go test ./...
.PHONY: test-unit

build: ## Build the executable. Example: make build
	@go version
	go build -mod=vendor $(DEBUGFLAGS)
.PHONY: build

vendor: ## Vendor dependencies. Example: make vendor
	go mod vendor
.PHONY: vendor

clean: ## Clean up the workspace. Example: make clean
	rm -f openshift-goimports
.PHONY: clean

help: ## Print this help. Example: make help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
.PHONY: help

