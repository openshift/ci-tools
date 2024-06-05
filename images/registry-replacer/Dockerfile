FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="muller@redhat.com"

RUN microdnf install -y git && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

ADD registry-replacer /usr/bin/registry-replacer
ENTRYPOINT ["/usr/bin/registry-replacer"]
