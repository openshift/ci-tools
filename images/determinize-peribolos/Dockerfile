FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

ADD determinize-peribolos /usr/bin/determinize-peribolos
ENTRYPOINT ["/usr/bin/determinize-peribolos"]
