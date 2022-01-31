FROM quay.io/centos/centos:stream8
LABEL maintainer="muller@redhat.com"

RUN yum install -y git && \
    yum clean all && \
    rm -rf /var/cache/yum

ADD registry-replacer /usr/bin/registry-replacer
ENTRYPOINT ["/usr/bin/registry-replacer"]
