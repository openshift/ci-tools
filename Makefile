# Old-skool build tools.
#
# Targets (see each target for more information):
#   all: Build code.
#   build: Build code.
#   test: Run all tests.
#   clean: Clean up.
#

OUT_DIR = _output
OS_OUTPUT_GOPATH ?= 1
SHELL=/usr/bin/env bash -eo pipefail

export GOFLAGS
export TESTFLAGS

# Tests run using `make` are most often run by the CI system, so we are OK to
# assume the user wants jUnit output and will turn it off if they don't.
JUNIT_REPORT ?= true

# Build code.
#
# Args:
#   WHAT: Directory names to build.  If any of these directories has a 'main'
#     package, the build will produce executable files under $(OUT_DIR)/local/bin.
#     If not specified, "everything" will be built.
#   GOFLAGS: Extra flags to pass to 'go' when building.
#   TESTFLAGS: Extra flags that should only be passed to hack/test-go.sh
#
# Example:
#   make
#   make all
#   make all WHAT=cmd/oc GOFLAGS=-v
all build:
	hack/build-go.sh $(WHAT) $(GOFLAGS)
.PHONY: all build

# Verify code conventions are properly setup.
#
# Example:
#   make verify
verify:
	{ \
	hack/verify-gofmt.sh ||r=1;\
	hack/verify-govet.sh ||r=1;\
	make verify-gen || rc=1;\
	exit $$r ;\
	}
.PHONY: verify

# Verify code conventions are properly setup.
#
# Example:
#   make lint
lint:
	./hack/lint.sh
.PHONY: lint

# Run unit tests.
#
# Args:
#   GOFLAGS: Extra flags to pass to 'go' when building.
#
# Example:
#   make test
test: cmd/vault-secret-collection-manager/index.js
	TESTFLAGS="$(TESTFLAGS)" hack/test-go.sh
.PHONY: test

# Remove all build artifacts.
#
# Example:
#   make clean
clean:
	rm -rf $(OUT_DIR)
.PHONY: clean

# Format all Go source code.
#
# Example:
#   make format
format: cmd/vault-secret-collection-manager/index.js
	gofmt -s -w $(shell go list -f '{{ .Dir }}' ./... )
.PHONY: format

# Update vendored code and manifests to ensure formatting.
#
# Example:
#   make update-vendor
update-vendor:
	docker run --rm \
		--user=$$UID \
		-v $$(go env GOCACHE):/.cache:Z \
		-v $$PWD:/go/src/github.com/openshift/ci-tools:Z \
		-w /go/src/github.com/openshift/ci-tools \
		-e GO111MODULE=on \
		-e GOPROXY=https://proxy.golang.org \
		-e GOCACHE=/tmp/go-build-cache \
		golang:1.16 \
		/bin/bash -c "go mod tidy && go mod vendor"
.PHONY: update-vendor

# Validate vendored code and manifests to ensure formatting.
#
# Example:
#   make validate-vendor
validate-vendor:
	go version
	GO111MODULE=on GOPROXY=https://proxy.golang.org go mod tidy
	GO111MODULE=on GOPROXY=https://proxy.golang.org go mod vendor
	git status -s ./vendor/ go.mod go.sum
	test -z "$$(git status -s ./vendor/ go.mod go.sum | grep -v vendor/modules.txt)"
.PHONY: validate-vendor

# Install Go binaries to $GOPATH/bin.
#
# Example:
#   make install
install:
	go install ./cmd/...
.PHONY: install

cmd/vault-secret-collection-manager/index.js: cmd/vault-secret-collection-manager/index.ts
	tsc --lib ES2015,dom cmd/vault-secret-collection-manager/index.ts

# Install Go binaries to $GOPATH/bin.
# Set version and name variables.
#
# Example:
#   make production-install
production-install: cmd/vault-secret-collection-manager/index.js
	hack/install.sh
.PHONY: production-install

# Install Go binaries with enabled race detector to $GOPATH/bin.
# Set version and name variables.
#
# Example:
#   make production-install
race-install: cmd/vault-secret-collection-manager/index.js
	hack/install.sh race

# Run integration tests.
#
# Accepts a specific suite to run as an argument.
#
# Example:
#   make integration
#   make integration SUITE=multi-stage
integration:
	# legacy, so we don't break them
	test/entrypoint-wrapper-integration.sh
	hack/test-integration.sh $(SUITE)
.PHONY: integration

TMPDIR ?= /tmp

# Run e2e tests.
#
# Accepts a specific suite to run as an argument.
#
# Example:
#   make e2e
#   make e2e SUITE=multi-stage
e2e:
	echo -n "u:p" > $(TMPDIR)/boskos-credentials
	BOSKOS_CREDENTIALS_FILE="$(TMPDIR)/boskos-credentials" PACKAGES="./test/e2e/..." TESTFLAGS="$(TESTFLAGS) -tags e2e -timeout 70m -parallel 100" hack/test-go.sh
.PHONY: e2e

CLUSTER ?= build01

# Dependencies required to execute the E2E tests outside of the CI environment.
local-e2e: \
	$(TMPDIR)/.ci-operator-kubeconfig \
	$(TMPDIR)/local-secret/.dockerconfigjson \
	$(TMPDIR)/remote-secret/.dockerconfigjson \
	$(TMPDIR)/gcs/service-account.json \
	$(TMPDIR)/boskos
	$(eval export KUBECONFIG=$(TMPDIR)/.ci-operator-kubeconfig)
	$(eval export LOCAL_REGISTRY_SECRET_DIR=$(TMPDIR)/local-secret)
	$(eval export REMOTE_REGISTRY_SECRET_DIR=$(TMPDIR)/remote-secret)
	$(eval export GCS_CREDENTIALS_FILE=$(TMPDIR)/gcs/service-account.json)
	$(eval export PATH=${PATH}:$(TMPDIR))
	@$(MAKE) e2e
.PHONY: local-e2e

# Update golden output files for integration tests.
#
# Example:
#   make update-integration
#   make update-integration SUITE=multi-stage
update-integration:
	UPDATE=true make integration
.PHONY: update-integration

kubeExport := "jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp)'"

pr-deploy-configresolver:
	$(eval USER=$(shell curl --fail -Ss https://api.github.com/repos/openshift/ci-tools/pulls/$(PULL_REQUEST)|jq -r .head.user.login))
	$(eval BRANCH=$(shell curl --fail -Ss https://api.github.com/repos/openshift/ci-tools/pulls/$(PULL_REQUEST)|jq -r .head.ref))
	oc --context app.ci --as system:admin process -p USER=$(USER) -p BRANCH=$(BRANCH) -p PULL_REQUEST=$(PULL_REQUEST) -f hack/pr-deploy.yaml | oc  --context app.ci --as system:admin apply -f -
	for cm in ci-operator-master-configs step-registry config; do oc  --context app.ci --as system:admin get configmap $${cm} -n ci -o json | eval $(kubeExport)|oc  --context app.ci --as system:admin create -f - -n ci-tools-$(PULL_REQUEST); done
	echo "server is at https://$$( oc  --context app.ci --as system:admin get route server -n ci-tools-$(PULL_REQUEST) -o jsonpath={.spec.host} )"
.PHONY: pr-deploy

pr-deploy-backporter:
	$(eval USER=$(shell curl --fail -Ss https://api.github.com/repos/openshift/ci-tools/pulls/$(PULL_REQUEST)|jq -r .head.user.login))
	$(eval BRANCH=$(shell curl --fail -Ss https://api.github.com/repos/openshift/ci-tools/pulls/$(PULL_REQUEST)|jq -r .head.ref))
	oc --context app.ci --as system:admin process -p USER=$(USER) -p BRANCH=$(BRANCH) -p PULL_REQUEST=$(PULL_REQUEST) -f hack/pr-deploy-backporter.yaml | oc  --context app.ci --as system:admin apply -f -
	oc  --context app.ci --as system:admin get configmap plugins -n ci -o json | eval $(kubeExport) | oc  --context app.ci --as system:admin create -f - -n ci-tools-$(PULL_REQUEST)
	oc  --context app.ci --as system:admin get secret bugzilla-credentials-openshift-bugzilla-robot -n ci -o json | eval $(kubeExport) | oc  --context app.ci --as system:admin create -f - -n ci-tools-$(PULL_REQUEST)
	echo "server is at https://$$( oc  --context app.ci --as system:admin get route bp-server -n ci-tools-$(PULL_REQUEST) -o jsonpath={.spec.host} )"
.PHONY: pr-deploy-backporter

pr-deploy-vault-secret-manager:
	$(eval USER=$(shell curl --fail -Ss https://api.github.com/repos/openshift/ci-tools/pulls/$(PULL_REQUEST)|jq -r .head.user.login))
	$(eval BRANCH=$(shell curl --fail -Ss https://api.github.com/repos/openshift/ci-tools/pulls/$(PULL_REQUEST)|jq -r .head.ref))
	oc --context app.ci --as system:admin process -p USER=$(USER) -p BRANCH=$(BRANCH) -p PULL_REQUEST=$(PULL_REQUEST) -f hack/pr-deploy-vault-secret-manager.yaml | oc  --context app.ci --as system:admin apply -f -
	kubectl patch  -n vault rolebinding registry-viewer --type=json --patch='[{"op":"replace", "path":"/subjects/1/namespace", "value":"ci-tools-$(PULL_REQUEST)"}]'
	echo "server is at https://$$( oc  --context app.ci --as system:admin get route vault-secret-collection-manager -n ci-tools-$(PULL_REQUEST) -o jsonpath={.spec.host} )"
.PHONY: pr-deploy-backporter

check-breaking-changes:
	test/validate-prowgen-breaking-changes.sh
.PHONY: check-breaking-changes

.PHONY: generate
generate:
	hack/update-codegen.sh
	hack/generate-ci-op-reference.sh
	go run  ./vendor/github.com/coreydaley/openshift-goimports/ -m github.com/openshift/ci-tools

.PHONY: verify-gen
verify-gen: generate
	@# Don't add --quiet here, it disables --exit code in the git 1.7 we have in CI, making this unusuable
	if  ! git diff --exit-code; then \
		echo "generated files are out of date, run make generate"; exit 1; \
	fi

update-unit:
	UPDATE=true go test ./...
.PHONY: update-unit

validate-registry-metadata:
	generate-registry-metadata -registry test/multistage-registry/registry
	git status -s ./test/multistage-registry/registry
	test -z "$$(git status -s ./test/multistage-registry/registry | grep registry)"
.PHONY: validate-registry-metadata

$(TMPDIR)/.ci-operator-kubeconfig:
	oc --context $(CLUSTER) --as system:admin --namespace ci serviceaccounts create-kubeconfig ci-operator > $(TMPDIR)/.ci-operator-kubeconfig

$(TMPDIR)/local-secret/.dockerconfigjson:
	mkdir -p $(TMPDIR)/local-secret
	oc --context $(CLUSTER) --as system:admin --namespace test-credentials get secret registry-pull-credentials -o 'jsonpath={.data.\.dockerconfigjson}' | base64 --decode | jq > $(TMPDIR)/local-secret/.dockerconfigjson

$(TMPDIR)/remote-secret/.dockerconfigjson:
	mkdir -p $(TMPDIR)/remote-secret
	oc --context $(CLUSTER) --as system:admin --namespace test-credentials get secret ci-pull-credentials -o 'jsonpath={.data.\.dockerconfigjson}' | base64 --decode | jq > $(TMPDIR)/remote-secret/.dockerconfigjson

$(TMPDIR)/gcs/service-account.json:
	mkdir -p $(TMPDIR)/gcs
	oc --context $(CLUSTER) --as system:admin --namespace test-credentials get secret gce-sa-credentials-gcs-publisher -o 'jsonpath={.data.service-account\.json}' | base64 --decode | jq > $(TMPDIR)/gcs/service-account.json

$(TMPDIR)/boskos:
	mkdir -p $(TMPDIR)/image
	oc image extract registry.ci.openshift.org/ci/boskos:latest --path /:$(TMPDIR)/image
	mv $(TMPDIR)/image/app $(TMPDIR)/boskos
	chmod +x $(TMPDIR)/boskos
	rm -rf $(TMPDIR)/image
