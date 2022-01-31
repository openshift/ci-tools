FROM quay.io/centos/centos:stream8
LABEL maintainer="muller@redhat.com"

RUN dnf install --nogpg -y diffutils && \
      dnf clean all

ADD sanitize-prow-jobs /usr/bin/sanitize-prow-jobs
ENTRYPOINT ["/usr/bin/sanitize-prow-jobs"]
