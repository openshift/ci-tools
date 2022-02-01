FROM quay.io/centos/centos:stream8
LABEL maintainer="nmoraiti@redhat.com"

RUN dnf install --nogpg -y git && \
      dnf clean all

ADD ci-operator-config-mirror /usr/bin/ci-operator-config-mirror
ENTRYPOINT ["/usr/bin/ci-operator-config-mirror"]
