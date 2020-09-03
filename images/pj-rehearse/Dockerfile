FROM centos:8
LABEL maintainer="nmoraiti@redhat.com"

RUN yum install -y git && yum clean all
ADD pj-rehearse /usr/bin/pj-rehearse
ENTRYPOINT ["/usr/bin/pj-rehearse"]
