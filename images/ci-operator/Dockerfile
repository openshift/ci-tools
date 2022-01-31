FROM quay.io/centos/centos:stream8
LABEL maintainer="skuznets@redhat.com"

RUN yum install -y git python2
RUN alternatives --set python /usr/bin/python2
ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
