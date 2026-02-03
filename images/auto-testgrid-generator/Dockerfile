FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ADD testgrid-config-generator /usr/bin/testgrid-config-generator
ADD auto-testgrid-generator /usr/bin/auto-testgrid-generator

RUN microdnf install -y git && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

ENTRYPOINT ["/usr/bin/auto-testgrid-generator"]
