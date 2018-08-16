.PHONY: all
all: check test build

.PHONY: check
check: ## Lint code
	golint ./cmd/...
	go vet ./cmd/...

.PHONY: build
build: ## Build binary
	go build -v -o ci-operator ./cmd/ci-operator

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: help
help:
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
