FROM quay.io/centos/centos:stream9
LABEL maintainer="jguzik@redhat.com"
RUN curl -L -o /bin/envoy https://github.com/envoyproxy/envoy/releases/download/v1.25.3/envoy-1.25.3-linux-x86_64 && chmod +x /bin/envoy
RUN curl -L -o /tmp/api_descriptor.pb https://github.com/kubernetes/test-infra/raw/master/prow/gangway/api_descriptor.pb

COPY ./config.yml /tmp/config.yml
EXPOSE 32001
EXPOSE 32000
ENTRYPOINT ["/bin/envoy", "-c", "/tmp/config.yml"]
