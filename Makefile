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

all build: install
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

# Format all source code.
#
# Example:
#   make format
format: frontend-format gofmt
.PHONY: format

# Format all Go source code.
#
# Example:
#   make gofmt
gofmt: cmd/vault-secret-collection-manager/index.js
	gofmt -s -w $(shell go list -f '{{ .Dir }}' ./... )
.PHONY: gofmt

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
		golang:1.17 \
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

# Use verbosity by default, allow users to opt out
VERBOSE := $(if $(QUIET),,-v )

# Install Go binaries to $GOPATH/bin.
#
# Example:
#   make install
install: cmd/vault-secret-collection-manager/index.js
	go install $(VERBOSE)./cmd/...
.PHONY: install

cmd/vault-secret-collection-manager/index.js: cmd/vault-secret-collection-manager/index.ts
	hack/compile-typescript.sh

# Install Go binaries to $GOPATH/bin.
# Set version and name variables.
#
# Example:
#   make production-install
production-install: cmd/vault-secret-collection-manager/index.js cmd/pod-scaler/frontend/dist cmd/repo-init/frontend/dist
	rm -f cmd/pod-scaler/frontend/dist/dummy # we keep this file in git to keep the thing compiling without static assets
	rm -f cmd/repo-init/frontend/dist/dummy
	hack/install.sh
.PHONY: production-install

# Install Go binaries with enabled race detector to $GOPATH/bin.
# Set version and name variables.
#
# Example:
#   make production-install
race-install: cmd/vault-secret-collection-manager/index.js cmd/pod-scaler/frontend/dist cmd/repo-init/frontend/dist
	hack/install.sh race

# Run integration tests.
#
# Accepts a specific suite to run as an argument.
#
# Example:
#   make integration
#   make integration SUITE=multi-stage
integration:
	@set -e; \
		if [[ -n $$OPENSHIFT_CI ]]; then count=25; else count=1; fi && \
		for try in $$(seq $$count); do \
			echo "Try $$try" && \
			hack/test-integration.sh $(SUITE) ; \
		done
.PHONY: integration

TMPDIR ?= /tmp
TAGS ?= e2e,e2e_framework

# Run e2e tests.
#
# Accepts a specific suite to run as an argument.
#
# Example:
#   make e2e
#   make e2e SUITE=multi-stage
e2e: $(TMPDIR)/.boskos-credentials
	BOSKOS_CREDENTIALS_FILE="$(TMPDIR)/.boskos-credentials" PACKAGES="./test/e2e/..." TESTFLAGS="$(TESTFLAGS) -tags $(TAGS) -timeout 70m -parallel 100" hack/test-go.sh
.PHONY: e2e

$(TMPDIR)/.boskos-credentials:
	echo -n "u:p" > $(TMPDIR)/.boskos-credentials

CLUSTER ?= build01

# Dependencies required to execute the E2E tests outside of the CI environment.
local-e2e: \
	$(TMPDIR)/.ci-operator-kubeconfig \
	$(TMPDIR)/hive-kubeconfig \
	$(TMPDIR)/local-secret/.dockerconfigjson \
	$(TMPDIR)/remote-secret/.dockerconfigjson \
	$(TMPDIR)/gcs/service-account.json \
	$(TMPDIR)/boskos \
	$(TMPDIR)/prometheus \
	$(TMPDIR)/promtool
	$(eval export KUBECONFIG=$(TMPDIR)/.ci-operator-kubeconfig)
	$(eval export HIVE_KUBECONFIG=$(TMPDIR)/hive-kubeconfig)
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
	go run ./cmd/determinize-prow-config -prow-config-dir test/integration/repo-init/expected/core-services/prow/02_config -sharded-plugin-config-base-dir test/integration/repo-init/expected/core-services/prow/02_config
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
	test/validate-generation-breaking-changes.sh
.PHONY: check-breaking-changes

.PHONY: generate
generate: imports
	hack/update-codegen.sh
	hack/generate-ci-op-reference.sh

.PHONY: imports
imports:
	go run ./vendor/github.com/coreydaley/openshift-goimports/ -m github.com/openshift/ci-tools

.PHONY: verify-gen
verify-gen: generate cmd/pod-scaler/frontend/dist/dummy # we need the dummy file to exist so there's no diff on it
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

validate-checkconfig:
	test/validate-checkconfig.sh
.PHONY: validate-checkconfig

$(TMPDIR)/.ci-operator-kubeconfig:
	oc --context $(CLUSTER) --as system:admin --namespace ci serviceaccounts create-kubeconfig ci-operator > $(TMPDIR)/.ci-operator-kubeconfig

$(TMPDIR)/hive-kubeconfig:
	oc --context $(CLUSTER) --as system:admin --namespace test-credentials get secret hive-hive-credentials -o 'jsonpath={.data.kubeconfig}' | base64 --decode > "$@"

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

local-pod-scaler: $(TMPDIR)/prometheus $(TMPDIR)/promtool cmd/pod-scaler/frontend/dist
	$(eval export PATH=${PATH}:$(TMPDIR))
	go run -tags e2e,e2e_framework ./test/e2e/pod-scaler/local/main.go
.PHONY: local-pod-scaler

.PHONY: cmd/pod-scaler/frontend/dist
cmd/pod-scaler/frontend/dist: cmd/pod-scaler/frontend/node_modules
	@$(MAKE) npm NPM_ARGS="run build"
	@$(MAKE) cmd/pod-scaler/frontend/dist/dummy

local-pod-scaler-ui: cmd/pod-scaler/frontend/node_modules $(HOME)/.cache/pod-scaler/steps/container_memory_working_set_bytes.json
	go run -tags e2e,e2e_framework ./test/e2e/pod-scaler/local/main.go --cache-dir $(HOME)/.cache/pod-scaler --serve-dev-ui
.PHONY: local-pod-scaler

$(HOME)/.cache/pod-scaler/steps/container_memory_working_set_bytes.json:
	mkdir -p $(HOME)/.cache/pod-scaler
	gsutil -m cp -r gs://origin-ci-resource-usage-data/* $(HOME)/.cache/pod-scaler

frontend-checks: cmd/pod-scaler/frontend/node_modules cmd/repo-init/frontend/node_modules
	@$(MAKE) npm NPM_ARGS="run ci-checks"
.PHONY: frontend-checks

cmd/pod-scaler/frontend/node_modules:
	@$(MAKE) npm NPM_ARGS="ci"

cmd/pod-scaler/frontend/dist/dummy:
	echo "file used to keep go embed happy" > cmd/pod-scaler/frontend/dist/dummy

.PHONY: frontend-format
frontend-format: cmd/pod-scaler/frontend/node_modules cmd/repo-init/frontend/node_modules
	@$(MAKE) npm NPM_ARGS="run format"

local-repo-init-api: cmd/repo-init/frontend/dist
	$(eval export PATH=${PATH}:$(TMPDIR))
	go run -tags e2e,e2e_framework ./test/e2e/repo-init/local/main.go
.PHONY: local-repo-init-ui

.PHONY: cmd/repo-init/frontend/dist
cmd/repo-init/frontend/dist: cmd/repo-init/frontend/node_modules
	@$(MAKE) npm NPM_ARGS="run build"
	@$(MAKE) cmd/repo-init/frontend/dist/dummy

cmd/repo-init/frontend/node_modules:
	@$(MAKE) npm NPM_ARGS="ci"

cmd/repo-init/frontend/dist/dummy:
	echo "file used to keep go embed happy" > cmd/repo-init/frontend/dist/dummy

ifdef CI
NPM_FLAGS = 'npm_config_cache=/go/.npm'
endif

.PHONY: npm
npm:
	env $(NPM_FLAGS) npm --prefix cmd/pod-scaler/frontend $(NPM_ARGS)

.PHONY: verify-frontend-format
verify-frontend-format: frontend-format
	@# Don't add --quiet here, it disables --exit code in the git 1.7 we have in CI, making this unusuable
	if  ! git diff --exit-code cmd/pod-scaler/frontend; then \
		echo "frontend files are not formatted, run make frontend-format"; exit 1; \
	fi

$(TMPDIR)/prometheus:
	mkdir -p $(TMPDIR)/image
	oc image extract quay.io/prometheus/prometheus:latest --path /bin/prometheus:$(TMPDIR)/image
	mv $(TMPDIR)/image/prometheus $(TMPDIR)/prometheus
	chmod +x $(TMPDIR)/prometheus
	rm -rf $(TMPDIR)/image

$(TMPDIR)/promtool:
	mkdir -p $(TMPDIR)/image
	oc image extract quay.io/prometheus/prometheus:main --path /bin/promtool:$(TMPDIR)/image
	mv $(TMPDIR)/image/promtool $(TMPDIR)/promtool
	chmod +x $(TMPDIR)/promtool
	rm -rf $(TMPDIR)/image

$(TMPDIR)/.promoted-image-governor-kubeconfig-dir:
	rm -rf $(TMPDIR)/.promoted-image-governor-kubeconfig-dir
	mkdir -p $(TMPDIR)/.promoted-image-governor-kubeconfig-dir
	oc --context app.ci --namespace ci extract secret/promoted-image-governor --confirm --to=$(TMPDIR)/.promoted-image-governor-kubeconfig-dir
	oc --context app.ci --namespace ci serviceaccounts create-kubeconfig promoted-image-governor | sed 's/promoted-image-governor/app.ci/g' > $(TMPDIR)/.promoted-image-governor-kubeconfig-dir/sa.promoted-image-governor.app.ci.config

release_folder := $$PWD/../release

promoted-image-governor: $(TMPDIR)/.promoted-image-governor-kubeconfig-dir
	go run  ./cmd/promoted-image-governor --kubeconfig-dir=$(TMPDIR)/.promoted-image-governor-kubeconfig-dir --ci-operator-config-path=$(release_folder)/ci-operator/config --release-controller-mirror-config-dir=$(release_folder)/core-services/release-controller/_releases --ignored-image-stream-tags='^ocp\S*/\S+:machine-os-content$$' --ignored-image-stream-tags='^openshift/origin-v3.11:' --dry-run=true
.PHONY: promoted-image-governor

explain: $(TMPDIR)/.promoted-image-governor-kubeconfig
	@[[ $$istag ]] || (echo "ERROR: \$$istag must be set"; exit 1)
	@go run  ./cmd/promoted-image-governor --kubeconfig=$(TMPDIR)/.promoted-image-governor-kubeconfig --ci-operator-config-path=$(release_folder)/ci-operator/config --release-controller-mirror-config-dir=$(release_folder)/core-services/release-controller/_releases --explain $(istag) --dry-run=true --log-level=fatal
.PHONY: explain


$(TMPDIR)/.github-ldap-user-group-creator-kubeconfig-dir:
	rm -rf $(TMPDIR)/.github-ldap-user-group-creator-kubeconfig-dir
	mkdir -p $(TMPDIR)/.github-ldap-user-group-creator-kubeconfig-dir
	oc --context app.ci --namespace ci extract secret/github-ldap-user-group-creator --confirm --to=$(TMPDIR)/.github-ldap-user-group-creator-kubeconfig-dir
	oc --context app.ci --namespace ci serviceaccounts create-kubeconfig github-ldap-user-group-creator | sed 's/github-ldap-user-group-creator/app.ci/g' > $(TMPDIR)/.github-ldap-user-group-creator-kubeconfig-dir/sa.github-ldap-user-group-creator.app.ci.config

github-ldap-user-group-creator: $(TMPDIR)/.github-ldap-user-group-creator-kubeconfig-dir
	@go run  ./cmd/github-ldap-user-group-creator --kubeconfig-dir=$(TMPDIR)/.github-ldap-user-group-creator-kubeconfig-dir --mapping-file=/tmp/mapping.yaml --dry-run=true --log-level=debug
.PHONY: github-ldap-user-group-creator
