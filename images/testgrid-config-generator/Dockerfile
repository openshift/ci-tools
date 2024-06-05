FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

ADD testgrid-config-generator /usr/bin/testgrid-config-generator
ENTRYPOINT ["/usr/bin/testgrid-config-generator"]
