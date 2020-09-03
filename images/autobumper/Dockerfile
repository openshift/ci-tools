FROM centos:8

ADD autobumper /usr/bin/autobumper

RUN yum install -y git make

ENTRYPOINT ["/usr/bin/autobumper"]
