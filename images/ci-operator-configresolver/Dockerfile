FROM quay.io/centos/centos:stream9
LABEL maintainer="skuznets@redhat.com"

ADD ci-operator-configresolver /usr/bin/ci-operator-configresolver
RUN yum -y install graphviz && yum clean all && rm -rf /var/cache/yum
ENTRYPOINT ["/usr/bin/ci-operator-configresolver"]
