FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="deads@redhat.com"

ADD job-run-aggregator /usr/bin/job-run-aggregator
ADD prcreator /usr/bin/prcreator
RUN microdnf install -y git
ENTRYPOINT ["/usr/bin/job-run-aggregator"]
