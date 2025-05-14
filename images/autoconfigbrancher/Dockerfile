FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ADD determinize-ci-operator /usr/bin/determinize-ci-operator
ADD config-brancher /usr/bin/config-brancher
ADD ci-operator-prowgen /usr/bin/ci-operator-prowgen
ADD autoconfigbrancher /usr/bin/autoconfigbrancher
ADD ci-operator-config-mirror /usr/bin/ci-operator-config-mirror
ADD private-prow-configs-mirror /usr/bin/private-prow-configs-mirror
ADD sanitize-prow-jobs /usr/bin/sanitize-prow-jobs
ADD determinize-prow-config /usr/bin/determinize-prow-config
ADD registry-replacer /usr/bin/registry-replacer
ADD ci-operator-yaml-creator /usr/bin/ci-operator-yaml-creator
ADD clusterimageset-updater /usr/bin/clusterimageset-updater
ADD promoted-image-governor /usr/bin/promoted-image-governor
#ADD rebalancer /usr/bin/rebalancer TODO: enable when tool merged

RUN microdnf install -y git && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

ENTRYPOINT ["/usr/bin/autoconfigbrancher"]
