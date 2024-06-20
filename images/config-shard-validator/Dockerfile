FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

RUN microdnf install -y git && \
      microdnf clean all

ADD config-shard-validator /usr/bin/config-shard-validator
ENTRYPOINT ["/usr/bin/config-shard-validator"]
