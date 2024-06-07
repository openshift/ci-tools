FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y git python3
RUN alternatives --install /usr/bin/python python /usr/bin/python3.9 1

ADD manifest-tool /usr/bin/manifest-tool
ADD ci-operator /usr/bin/ci-operator
ENTRYPOINT ["/usr/bin/ci-operator"]
