FROM quay.io/centos/centos:stream8
LABEL maintainer="nmoraiti@redhat.com"

RUN yum install -y git && yum clean all
RUN git config --global core.compression 9

ADD private-org-sync /usr/bin/private-org-sync
ENTRYPOINT ["/usr/bin/private-org-sync"]
