FROM centos:7
LABEL maintainer="skuznets@redhat.com"

RUN dnf install --nogpg -y diffutils && \
      dnf clean all

ADD determinize-ci-operator /usr/bin/determinize-ci-operator
ENTRYPOINT ["/usr/bin/determinize-ci-operator"]
