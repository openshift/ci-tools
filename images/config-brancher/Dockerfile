FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

ADD config-brancher /usr/bin/config-brancher
ENTRYPOINT ["/usr/bin/config-brancher"]
