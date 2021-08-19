FROM centos:8
LABEL maintainer="skuznets@redhat.com"

RUN dnf install --nogpg -y git && \
      dnf clean all

ADD ci-operator-checkconfig /usr/bin/ci-operator-checkconfig
ENTRYPOINT ["/usr/bin/ci-operator-checkconfig"]
