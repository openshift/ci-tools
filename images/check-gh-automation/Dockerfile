FROM quay.io/centos/centos:stream8
LABEL maintainer="sgoeddel@redhat.com"

RUN yum install -y git && yum clean all
ADD check-gh-automation /usr/bin/check-gh-automation
ENTRYPOINT ["/usr/bin/check-gh-automation"]
