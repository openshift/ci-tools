FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="jguzik@redhat.com"

ADD tide-config-manager /usr/bin/tide-config-manager
ENTRYPOINT ["/usr/bin/tide-config-manager"]
