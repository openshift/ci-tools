FROM centos:7
LABEL maintainer="hongkliu@redhat.com"

COPY usr/bin/oc /usr/bin/oc

RUN yum install -y cronie openssl && \
      yum clean all && \
      rm -rf /var/cache/yum && \
      curl https://get.acme.sh | sh
