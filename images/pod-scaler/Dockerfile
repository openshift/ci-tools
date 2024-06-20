FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

ADD pod-scaler /usr/bin/pod-scaler
ENTRYPOINT ["/usr/bin/pod-scaler"]
