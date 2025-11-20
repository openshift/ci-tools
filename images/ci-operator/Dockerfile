FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y git python3 findutils tar jq && \
    ARCH=$(uname -m) && \
    case "$ARCH" in \
        x86_64) GOARCH=amd64 ;; \
        aarch64) GOARCH=arm64 ;; \
        *) echo "Unsupported architecture: $ARCH" && exit 1 ;; \
    esac && \
    curl -L https://go.dev/dl/go1.24.0.linux-${GOARCH}.tar.gz -o /tmp/go.tar.gz && \
    tar -C /usr/local -xzf /tmp/go.tar.gz && \
    rm /tmp/go.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"

ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
