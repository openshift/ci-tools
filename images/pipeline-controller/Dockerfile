FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="jguzik@redhat.com"

ADD pipeline-controller /usr/bin/pipeline-controller
ENTRYPOINT ["/usr/bin/pipeline-controller"]
