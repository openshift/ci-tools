FROM quay.io/centos/centos:stream8

LABEL maintainer="nmoraiti@redhat.com"

RUN yum install -y git
ADD publicize /usr/bin/publicize

ENTRYPOINT ["/usr/bin/publicize"]
