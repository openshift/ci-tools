FROM centos:7
LABEL maintainer="skuznets@redhat.com"

RUN yum install -y git
ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
