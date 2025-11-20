FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y git python3 findutils tar jq && \
    curl -L https://go.dev/dl/go1.24.0.linux-amd64.tar.gz -o /tmp/go1.24.0.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf /tmp/go1.24.0.linux-amd64.tar.gz && \
    rm /tmp/go1.24.0.linux-amd64.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"

ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
