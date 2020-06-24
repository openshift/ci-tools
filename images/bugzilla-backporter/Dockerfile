FROM centos:7
LABEL maintainer="skuznets@redhat.com"

ADD bugzilla-backporter /usr/bin/bugzilla-backporter
RUN yum -y install backporter && yum clean all && rm -rf /var/cache/yum
ENTRYPOINT ["/usr/bin/bugzilla-backporter"]