FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ADD autoowners /usr/bin/autoowners

RUN microdnf install -y git

ENTRYPOINT ["/usr/bin/autoowners"]
