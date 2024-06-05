FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="jchaloup@redhat.com"

ADD result-aggregator /usr/bin/result-aggregator
ENTRYPOINT ["/usr/bin/result-aggregator"]
