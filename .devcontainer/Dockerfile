FROM registry.access.redhat.com/ubi9/go-toolset:1.26

USER root

RUN dnf install -y 'dnf-command(config-manager)' && \
    dnf config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo && \
    dnf install -y git make jq gh python3 python3-pip openssl nodejs npm && \
    dnf clean all

# make test/verify compile the vault-secret-collection-manager front-end and fail without tsc when CI is set
RUN npm install -g typescript && npm cache clean --force
# make lint calls golangci-lint directly when CI is set
ARG GOLANGCI_LINT_VERSION=2.10.1
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "aarch64" ]; then LINT_ARCH="arm64"; else LINT_ARCH="amd64"; fi && \
    rpm -i "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${LINT_ARCH}.rpm"

RUN useradd --create-home --uid 1000 vscode && \
    git config --system --add safe.directory /workspace

# The pod runs with an arbitrary uid in group 0
RUN chgrp -R 0 /home/vscode && chmod -R g=u /home/vscode

# The base image sets HOME to /opt/app-root/src, which is not writable by this user
ENV HOME=/home/vscode \
    GOPATH=/home/vscode/go \
    GOCACHE=/home/vscode/.cache/go-build

USER vscode
