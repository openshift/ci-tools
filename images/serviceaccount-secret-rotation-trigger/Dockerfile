FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="muller@redhat.com"

ADD serviceaccount-secret-rotation-trigger /usr/bin/serviceaccount-secret-rotation-trigger
ENTRYPOINT ["/usr/bin/serviceaccount-secret-rotation-trigger"]
