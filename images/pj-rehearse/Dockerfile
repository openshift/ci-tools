FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="nmoraiti@redhat.com"

RUN microdnf install -y git && microdnf clean all
ADD pj-rehearse /usr/bin/pj-rehearse
ADD /usr/bin/oc /usr/bin/oc
ENTRYPOINT ["/usr/bin/pj-rehearse"]
