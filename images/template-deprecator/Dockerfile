FROM quay.io/centos/centos:stream9
LABEL maintainer="muller@redhat.com"

RUN dnf install -y diffutils && \
      dnf clean all

ADD template-deprecator /usr/bin/template-deprecator
ENTRYPOINT ["/usr/bin/template-deprecator"]
