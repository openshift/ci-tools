FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ADD efs-cluster-writer /usr/bin/efs-cluster-writer

RUN microdnf install -y ca-certificates && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

USER 1000:1000
ENTRYPOINT ["/usr/bin/efs-cluster-writer"]
