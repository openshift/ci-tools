FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="nmoraiti@redhat.com"

RUN microdnf install -y git && \
      microdnf clean all

ADD ci-operator-config-mirror /usr/bin/ci-operator-config-mirror
ENTRYPOINT ["/usr/bin/ci-operator-config-mirror"]
