FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="sgoeddel@redhat.com"

RUN microdnf install -y git diffutils && microdnf clean all
ADD cluster-init /usr/bin/cluster-init
ENTRYPOINT ["/usr/bin/cluster-init"]
