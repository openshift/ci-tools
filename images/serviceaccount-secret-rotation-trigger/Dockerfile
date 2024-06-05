FROM quay.io/centos/centos:stream9
LABEL maintainer="muller@redhat.com"

ADD serviceaccount-secret-rotation-trigger /usr/bin/serviceaccount-secret-rotation-trigger
ENTRYPOINT ["/usr/bin/serviceaccount-secret-rotation-trigger"]
