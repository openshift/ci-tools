FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ADD private-org-peribolos-sync /usr/bin/private-org-peribolos-sync
ADD autoperibolossync /usr/bin/autoperibolossync

RUN microdnf install -y git && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

ENTRYPOINT ["/usr/bin/autoperibolossync"]
