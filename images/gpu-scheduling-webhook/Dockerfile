FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="dgemoli@redhat.com"

ADD gpu-scheduling-webhook /usr/bin/gpu-scheduling-webhook
ENTRYPOINT ["/usr/bin/gpu-scheduling-webhook"]