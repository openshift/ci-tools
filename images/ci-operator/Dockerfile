FROM centos:8
LABEL maintainer="skuznets@redhat.com"

RUN yum install -y git
ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
