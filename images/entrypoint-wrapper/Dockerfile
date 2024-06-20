FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="bbarcaro@redhat.com"

ADD entrypoint-wrapper /usr/bin/entrypoint-wrapper
ENTRYPOINT ["/usr/bin/entrypoint-wrapper"]