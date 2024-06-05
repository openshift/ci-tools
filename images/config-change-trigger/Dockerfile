FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

RUN microdnf install -y git && microdnf clean all
ADD config-change-trigger /usr/bin/config-change-trigger
ENTRYPOINT ["/usr/bin/config-change-trigger"]