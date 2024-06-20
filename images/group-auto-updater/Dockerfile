FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="nmoraiti@redhat.com"

ADD group-auto-updater /usr/bin/group-auto-updater
ENTRYPOINT ["/usr/bin/group-auto-updater"]