FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

RUN microdnf install -y git && \
      microdnf clean all

ADD ci-operator-checkconfig /usr/bin/ci-operator-checkconfig
ENTRYPOINT ["/usr/bin/ci-operator-checkconfig"]
