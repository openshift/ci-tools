FROM quay.io/centos/centos:stream9
LABEL maintainer="deads@redhat.com"

ADD job-run-aggregator /usr/bin/job-run-aggregator
ADD prcreator /usr/bin/prcreator
RUN dnf install -y git
ENTRYPOINT ["/usr/bin/job-run-aggregator"]
