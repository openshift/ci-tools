FROM quay.io/centos/centos:stream8
LABEL maintainer="sgoeddel@redhat.com"

ADD repo-init /usr/bin/repo-init

RUN yum install -y git && \
    yum clean all && \
    rm -rf /var/cache/yum

ENTRYPOINT ["/usr/bin/repo-init"]
