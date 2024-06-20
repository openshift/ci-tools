FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ADD ci-secret-bootstrap /usr/bin/ci-secret-bootstrap
ENTRYPOINT ["/usr/bin/ci-secret-bootstrap"]
