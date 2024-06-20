FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="muller@redhat.com"

ADD applyconfig /usr/bin/applyconfig
ADD usr/bin/oc /usr/bin/oc
ENTRYPOINT ["/usr/bin/applyconfig"]
