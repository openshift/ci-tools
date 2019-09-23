FROM centos:7
LABEL maintainer="skuznets@redhat.com"

RUN yum install -y git && yum clean all
ADD config-change-trigger /usr/bin/config-change-trigger
ENTRYPOINT ["/usr/bin/config-change-trigger"]