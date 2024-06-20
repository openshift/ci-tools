FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="nmoraiti@redhat.com"

RUN microdnf install -y git && microdnf clean all
RUN git config --global core.compression 9

ADD private-org-sync /usr/bin/private-org-sync
ENTRYPOINT ["/usr/bin/private-org-sync"]
