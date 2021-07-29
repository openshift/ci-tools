FROM centos:8

ADD autoclusterimagesetupdater /usr/bin/autoclusterimagesetupdater

RUN yum install -y git

ENTRYPOINT ["/usr/bin/clusterimageset-updater"]
