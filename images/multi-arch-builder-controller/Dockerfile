FROM quay.io/centos/centos:stream9

ADD manifest-tool /usr/bin/manifest-tool
ADD multi-arch-builder-controller /usr/bin/multi-arch-builder-controller
ADD oc /usr/bin/oc
ENTRYPOINT ["/usr/bin/multi-arch-builder-controller"]
