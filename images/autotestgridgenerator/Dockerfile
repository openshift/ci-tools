FROM quay.io/centos/centos:stream8
LABEL maintainer="skuznets@redhat.com"

ADD testgrid-config-generator /usr/bin/testgrid-config-generator
ADD autotestgridgenerator /usr/bin/autotestgridgenerator

RUN dnf install -y git && \
    dnf clean all && \
    rm -rf /var/cache/dnf

ENTRYPOINT ["/usr/bin/autotestgridgenerator"]