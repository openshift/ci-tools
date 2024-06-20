FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="muller@redhat.com"

RUN microdnf install -y diffutils && \
      microdnf clean all

ADD template-deprecator /usr/bin/template-deprecator
ENTRYPOINT ["/usr/bin/template-deprecator"]
