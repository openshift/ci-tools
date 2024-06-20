FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

ADD repo-brancher /usr/bin/repo-brancher
ENTRYPOINT ["/usr/bin/repo-brancher"]
