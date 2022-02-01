FROM quay.io/centos/centos:stream8
LABEL maintainer="muller@redhat.com"

RUN yum install -y git && yum clean all
ADD private-org-sync /usr/bin/private-org-sync
ENTRYPOINT ["/usr/bin/private-org-sync"]
