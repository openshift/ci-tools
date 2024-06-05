FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

ADD testgrid-config-generator /usr/bin/testgrid-config-generator
ADD autotestgridgenerator /usr/bin/autotestgridgenerator

RUN microdnf install -y git && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

ENTRYPOINT ["/usr/bin/autotestgridgenerator"]