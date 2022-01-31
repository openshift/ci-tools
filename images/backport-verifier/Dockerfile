FROM quay.io/centos/centos:stream8

LABEL maintainer="skuznets@redhat.com"

ADD backport-verifier /usr/bin/backport-verifier

ENTRYPOINT ["/usr/bin/backport-verifier"]
