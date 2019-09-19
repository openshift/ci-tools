FROM centos:7

ADD determinize-ci-operator /usr/bin/determinize-ci-operator
ADD config-brancher /usr/bin//go/bin/config-brancher
ADD ci-operator-prowgen /usr/bin/ci-operator-prowgen

RUN yum install -y git && \
    yum clean all && \
    rm -rf /var/cache/yum

### CMD will be injected at the runtime
