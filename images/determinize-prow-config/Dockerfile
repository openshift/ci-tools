FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y diffutils && \
      microdnf clean all

ADD determinize-prow-config /usr/bin/determinize-prow-config
ENTRYPOINT ["/usr/bin/determinize-prow-config"]
