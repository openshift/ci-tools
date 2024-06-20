FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ADD prow-job-dispatcher /usr/bin/prow-job-dispatcher
ADD sanitize-prow-jobs /usr/bin/sanitize-prow-jobs

RUN microdnf install -y git && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

ENTRYPOINT ["/usr/bin/prow-job-dispatcher"]
