FROM quay.io/centos/centos:stream8
LABEL maintainer="apavel@redhat.com"

RUN yum install -y diffutils && \
    yum clean all && \
    rm -rf /var/cache/yum

ADD generate-registry-metadata /usr/bin/generate-registry-metadata
ENTRYPOINT ["/usr/bin/generate-registry-metadata"]
