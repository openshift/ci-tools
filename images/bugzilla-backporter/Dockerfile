FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

ADD bugzilla-backporter /usr/bin/bugzilla-backporter
ENTRYPOINT ["/usr/bin/bugzilla-backporter"]