FROM centos:8

ARG JQ_VERSION=1.6

COPY usr/bin/oc /usr/bin/oc

COPY google-cloud-sdk.repo /etc/yum.repos.d/google-cloud-sdk.repo

RUN  \
	dnf install -y nodejs jq google-cloud-sdk && \
	npm install -g @bitwarden/cli

ADD ci-secret-generator /usr/bin/ci-secret-generator

ENTRYPOINT ["/usr/bin/ci-secret-generator"]
