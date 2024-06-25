FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="sgoeddel@redhat.com"

RUN microdnf install -y tar gzip

ADD pr-reminder /usr/bin/pr-reminder
ENTRYPOINT ["/usr/bin/pr-reminder"]
