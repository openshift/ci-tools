FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

LABEL maintainer="nmoraiti@redhat.com"

RUN microdnf install -y git
ADD publicize /usr/bin/publicize

ENTRYPOINT ["/usr/bin/publicize"]
