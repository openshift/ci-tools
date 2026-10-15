FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

COPY gsm-proxy /usr/bin/gsm-proxy
USER 1001
ENTRYPOINT ["/usr/bin/gsm-proxy"]
