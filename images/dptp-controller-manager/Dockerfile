FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="aaleman@<<this-products-parent-company-name>>.com"

ADD dptp-controller-manager /usr/bin/dptp-controller-manager

ENTRYPOINT ["/usr/bin/dptp-controller-manager"]
