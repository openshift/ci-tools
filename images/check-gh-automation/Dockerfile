FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="sgoeddel@redhat.com"

RUN microdnf install -y git && microdnf clean all
ADD check-gh-automation /usr/bin/check-gh-automation
ENTRYPOINT ["/usr/bin/check-gh-automation"]
