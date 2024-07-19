FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y diffutils git && \
      microdnf clean all

ADD ci-operator-prowgen /usr/bin/ci-operator-prowgen
ENTRYPOINT ["/usr/bin/ci-operator-prowgen"]
