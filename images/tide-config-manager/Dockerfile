FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="jguzik@redhat.com"

RUN dnf install -y diffutils
ADD tide-config-manager /usr/bin/tide-config-manager
ADD determinize-prow-config /usr/bin/determinize-prow-config

ENTRYPOINT ["/usr/bin/tide-config-manager"]
