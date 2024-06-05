FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="apavel@redhat.com"

RUN microdnf install -y diffutils && \
      microdnf clean all

ADD clusterimageset-updater /usr/bin/clusterimageset-updater

ENTRYPOINT ["/usr/bin/clusterimageset-updater"]
