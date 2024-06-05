FROM quay.io/centos/centos:stream9
LABEL maintainer="nmoraiti@redhat.com"

RUN yum install -y git && yum clean all
ADD pj-rehearse /usr/bin/pj-rehearse
ADD /usr/bin/oc /usr/bin/oc
ENTRYPOINT ["/usr/bin/pj-rehearse"]
