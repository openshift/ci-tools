FROM mplatform/manifest-tool as builder
FROM quay.io/centos/centos:stream8

COPY --from=builder /manifest-tool /usr/bin/manifest-tool

RUN yum install -y git python2
RUN alternatives --set python /usr/bin/python2
ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
