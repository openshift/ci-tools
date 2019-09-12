FROM centos:7

ADD autoowners /usr/bin/autoowners

RUN yum install -y git

ENTRYPOINT ["/usr/bin/autoowners"]
