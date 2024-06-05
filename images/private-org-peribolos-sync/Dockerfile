FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="nmoraiti@redhat.com"

ADD private-org-peribolos-sync /usr/bin/private-org-peribolos-sync
ENTRYPOINT ["/usr/bin/private-org-peribolos-sync"]
