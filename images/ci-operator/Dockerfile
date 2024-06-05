FROM quay.io/centos/centos:stream9

RUN yum install -y git python2
RUN alternatives --set python /usr/bin/python2

ADD manifest-tool /usr/bin/manifest-tool
ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
