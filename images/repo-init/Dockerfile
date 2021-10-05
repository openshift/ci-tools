FROM centos:8
LABEL maintainer="eberglin@redhat.com"

ADD repo-init /usr/bin/repo-init

RUN yum install -y git && \
    yum clean all && \
    rm -rf /var/cache/yum

ENTRYPOINT ["/usr/bin/repo-init"]
