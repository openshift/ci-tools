FROM quay.io/centos/centos:stream8

ADD prow-job-dispatcher /usr/bin/prow-job-dispatcher
ADD sanitize-prow-jobs /usr/bin/sanitize-prow-jobs

RUN dnf install -y git && \
    dnf clean all && \
    rm -rf /var/cache/dnf

ENTRYPOINT ["/usr/bin/prow-job-dispatcher"]
