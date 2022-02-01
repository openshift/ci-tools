FROM quay.io/centos/centos:stream8

RUN yum install -y git && \
    yum clean all && \
    rm -rf /var/cache/yum

ADD ocp-build-data-enforcer /usr/bin/ocp-build-data-enforcer
ENTRYPOINT ["ocp-build-data-enforcer"]
