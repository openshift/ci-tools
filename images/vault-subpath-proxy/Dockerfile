FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="muller@redhat.com"

ADD vault-subpath-proxy /usr/bin/vault-subpath-proxy
ENTRYPOINT ["/usr/bin/vault-subpath-proxy"]
