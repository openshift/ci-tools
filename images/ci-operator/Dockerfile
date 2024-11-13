FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y git python3 findutils tar jq

ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
