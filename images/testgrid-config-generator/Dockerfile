FROM quay.io/centos/centos:stream9
LABEL maintainer="skuznets@redhat.com"

ADD testgrid-config-generator /usr/bin/testgrid-config-generator
ENTRYPOINT ["/usr/bin/testgrid-config-generator"]
