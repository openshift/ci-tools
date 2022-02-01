FROM quay.io/centos/centos:stream8
LABEL maintainer="nmoraiti@redhat.com"

ADD autopublicizeconfig /usr/bin/autopublicizeconfig

RUN yum install -y git && \
    yum clean all && \
    rm -rf /var/cache/yum

ENTRYPOINT ["/usr/bin/autopublicizeconfig"]
