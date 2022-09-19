FROM quay.io/centos/centos:stream8

ARG JQ_VERSION=1.6

COPY usr/bin/oc /usr/bin/oc

COPY google-cloud-sdk.repo /etc/yum.repos.d/google-cloud-sdk.repo
COPY oc_sa_create_kubeconfig.sh /usr/bin/oc_sa_create_kubeconfig.sh
COPY oc_create_kubeconfig.sh /usr/bin/oc_create_kubeconfig.sh

RUN dnf install -y jq google-cloud-sdk

ADD ci-secret-generator /usr/bin/ci-secret-generator

ENTRYPOINT ["/usr/bin/ci-secret-generator"]
