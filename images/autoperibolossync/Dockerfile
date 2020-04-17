FROM centos:8

ADD private-org-peribolos-sync /usr/bin/private-org-peribolos-sync

RUN yum install -y git && \
    yum clean all && \
    rm -rf /var/cache/yum

ENTRYPOINT ["/usr/bin/autoperibolossync"]
