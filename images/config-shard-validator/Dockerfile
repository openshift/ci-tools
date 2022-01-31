FROM quay.io/centos/centos:stream8
LABEL maintainer="skuznets@redhat.com"

RUN dnf install --nogpg -y git && \
      dnf clean all

ADD config-shard-validator /usr/bin/config-shard-validator
ENTRYPOINT ["/usr/bin/config-shard-validator"]
