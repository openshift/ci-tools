FROM centos:8

ARG JQ_VERSION=1.6

COPY usr/bin/oc /usr/bin/oc

RUN dnf install -y nodejs jq && \
    npm install -g @bitwarden/cli

ADD ci-secret-generator /usr/bin/ci-secret-generator

ENTRYPOINT ["/usr/bin/ci-secret-generator"]
