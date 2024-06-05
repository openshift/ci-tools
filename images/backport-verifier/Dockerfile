FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

LABEL maintainer="skuznets@redhat.com"

ADD backport-verifier /usr/bin/backport-verifier

ENTRYPOINT ["/usr/bin/backport-verifier"]
