FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="muller@redhat.com"

RUN microdnf install -y diffutils && \
      microdnf clean all

ADD sanitize-prow-jobs /usr/bin/sanitize-prow-jobs
ENTRYPOINT ["/usr/bin/sanitize-prow-jobs"]
