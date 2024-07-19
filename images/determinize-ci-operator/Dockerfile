FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y diffutils git && \
      microdnf clean all

ADD determinize-ci-operator /usr/bin/determinize-ci-operator
ENTRYPOINT ["/usr/bin/determinize-ci-operator"]
