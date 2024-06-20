FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="sgoeddel@redhat.com"

ADD repo-init /usr/bin/repo-init

ADD ci-operator-checkconfig /usr/bin/ci-operator-checkconfig
ADD ci-operator-prowgen /usr/bin/ci-operator-prowgen
ADD sanitize-prow-jobs /usr/bin/sanitize-prow-jobs

RUN microdnf install -y git && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

ENTRYPOINT ["/usr/bin/repo-init"]
