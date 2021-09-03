FROM centos:8
LABEL maintainer="apavel@redhat.com"

RUN dnf install --nogpg -y diffutils && \
      dnf clean all

ADD clusterimageset-updater /usr/bin/clusterimageset-updater

ENTRYPOINT ["/usr/bin/clusterimageset-updater"]
