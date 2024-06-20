FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ADD manifest-tool /usr/bin/manifest-tool
ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
