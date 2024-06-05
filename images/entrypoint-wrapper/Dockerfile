FROM quay.io/centos/centos:stream9
LABEL maintainer="bbarcaro@redhat.com"

ADD entrypoint-wrapper /usr/bin/entrypoint-wrapper
ENTRYPOINT ["/usr/bin/entrypoint-wrapper"]