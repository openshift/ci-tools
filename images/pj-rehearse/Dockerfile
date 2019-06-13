FROM centos:7
LABEL maintainer="nmoraiti@redhat.com"

RUN yum install -y git
ADD pj-rehearse /usr/bin/pj-rehearse
ENTRYPOINT ["/usr/bin/pj-rehearse"]
