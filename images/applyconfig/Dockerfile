FROM quay.io/centos/centos:stream9
LABEL maintainer="muller@redhat.com"

ADD applyconfig /usr/bin/applyconfig
ADD usr/bin/oc /usr/bin/oc
ENTRYPOINT ["/usr/bin/applyconfig"]
