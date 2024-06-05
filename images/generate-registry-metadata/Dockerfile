FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="apavel@redhat.com"

RUN microdnf install -y diffutils && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

ADD generate-registry-metadata /usr/bin/generate-registry-metadata
ENTRYPOINT ["/usr/bin/generate-registry-metadata"]
