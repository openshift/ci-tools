FROM quay.io/centos/centos:stream8
LABEL maintainer="skuznets@redhat.com"

RUN dnf install --nogpg -y diffutils git && \
      dnf clean all

ADD ci-operator-prowgen /usr/bin/ci-operator-prowgen
ENTRYPOINT ["/usr/bin/ci-operator-prowgen"]
