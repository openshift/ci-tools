FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="muller@redhat.com"

ADD cvp-trigger /usr/bin/cvp-trigger
ENTRYPOINT ["/usr/bin/cvp-trigger"]
