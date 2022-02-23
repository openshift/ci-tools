FROM quay.io/centos/centos:stream8
LABEL maintainer="sgoeddel@redhat.com"

ADD repo-init /usr/bin/repo-init

ADD ci-operator-checkconfig /usr/bin/ci-operator-checkconfig
ADD ci-operator-prowgen /usr/bin/ci-operator-prowgen
ADD sanitize-prow-jobs /usr/bin/sanitize-prow-jobs

RUN yum install -y git && \
    yum clean all && \
    rm -rf /var/cache/yum

ENTRYPOINT ["/usr/bin/repo-init"]
