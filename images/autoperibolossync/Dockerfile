FROM quay.io/centos/centos:stream8

ADD private-org-peribolos-sync /usr/bin/private-org-peribolos-sync
ADD autoperibolossync /usr/bin/autoperibolossync

RUN yum install -y git && \
    yum clean all && \
    rm -rf /var/cache/yum

ENTRYPOINT ["/usr/bin/autoperibolossync"]
