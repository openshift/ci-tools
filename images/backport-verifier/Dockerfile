FROM quay.io/centos/centos:stream9

LABEL maintainer="skuznets@redhat.com"

ADD backport-verifier /usr/bin/backport-verifier

ENTRYPOINT ["/usr/bin/backport-verifier"]
