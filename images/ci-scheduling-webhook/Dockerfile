FROM quay.io/centos/centos:stream9
LABEL maintainer="jupierce@redhat.com"
ADD ci-scheduling-webhook /usr/bin/ci-scheduling-webhook
ENTRYPOINT ["/usr/bin/ci-scheduling-webhook", "--port", "8443"]