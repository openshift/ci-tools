FROM quay.io/centos/centos:stream8
LABEL maintainer="muller@redhat.com"

RUN dnf install -y diffutils && \
      dnf clean all

ADD template-deprecator /usr/bin/template-deprecator
ENTRYPOINT ["/usr/bin/template-deprecator"]
