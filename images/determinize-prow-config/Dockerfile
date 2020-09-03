FROM centos:7
LABEL maintainer="skuznets@redhat.com"

RUN dnf install --nogpg -y diffutils && \
      dnf clean all

ADD determinize-prow-config /usr/bin/determinize-prow-config
ENTRYPOINT ["/usr/bin/determinize-prow-config"]
