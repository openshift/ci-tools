FROM centos:8
LABEL maintainer="sgoeddel@redhat.com"

RUN yum install -y git && yum clean all
ADD cluster-init /usr/bin/cluster-init
ENTRYPOINT ["/usr/bin/cluster-init"]
