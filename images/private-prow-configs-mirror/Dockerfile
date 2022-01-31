FROM quay.io/centos/centos:stream8
LABEL maintainer="nmoraiti@redhat.com"

RUN dnf install --nogpg -y git && \
      dnf clean all

ADD private-prow-configs-mirror /usr/bin/private-prow-configs-mirror
ENTRYPOINT ["/usr/bin/private-prow-configs-mirror"]
