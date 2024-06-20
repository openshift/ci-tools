FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y git && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

ADD ocp-build-data-enforcer /usr/bin/ocp-build-data-enforcer
ENTRYPOINT ["ocp-build-data-enforcer"]
