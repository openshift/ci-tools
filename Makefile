.PHONY: all
all: check test build

DOCKER_CMD := docker run --rm -v "$(PWD)":/go/src/github.com/openshift/ci-operator:Z -w /go/src/github.com/openshift/ci-operator golang:1.10

.PHONY: check
check: ## Lint code
	@echo -e "\033[32mRunning golint...\033[0m"
#	go get -u github.com/golang/lint # TODO figure out how to install when there is no golint
	golint ./cmd/...
	@echo -e "\033[32mRunning go vet...\033[0m"
	$(DOCKER_CMD) go vet ./cmd/...

.PHONY: build
build: ## Build binary
	@echo -e "\033[32mBuilding package...\033[0m"
	mkdir -p bin
	$(DOCKER_CMD) go build -v -o bin/ci-operator cmd/ci-operator/main.go

.PHONY: test
test: ## Run tests
	@echo -e "\033[32mTesting...\033[0m"
	$(DOCKER_CMD) go test ./...

.PHONY: help
help:
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
