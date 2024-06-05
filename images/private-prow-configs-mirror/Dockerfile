FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="nmoraiti@redhat.com"

RUN microdnf install -y git && \
      microdnf clean all

ADD private-prow-configs-mirror /usr/bin/private-prow-configs-mirror
ENTRYPOINT ["/usr/bin/private-prow-configs-mirror"]
