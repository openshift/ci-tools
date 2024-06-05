FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

ADD blocking-issue-creator /usr/bin/blocking-issue-creator
ENTRYPOINT ["/usr/bin/blocking-issue-creator"]
