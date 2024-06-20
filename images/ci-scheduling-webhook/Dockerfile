FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="jupierce@redhat.com"
ADD ci-scheduling-webhook /usr/bin/ci-scheduling-webhook
ENTRYPOINT ["/usr/bin/ci-scheduling-webhook", "--port", "8443"]