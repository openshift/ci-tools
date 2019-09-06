FROM centos:7

ADD autobumper /usr/bin/autobumper

RUN yum install -y git

ENTRYPOINT ["/usr/bin/autobumper"]
