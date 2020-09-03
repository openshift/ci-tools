FROM centos:8

ADD autoowners /usr/bin/autoowners

RUN yum install -y git

ENTRYPOINT ["/usr/bin/autoowners"]
