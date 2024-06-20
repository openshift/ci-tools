FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ADD manifest-tool /usr/bin/manifest-tool
ADD multi-arch-builder-controller /usr/bin/multi-arch-builder-controller
ADD oc /usr/bin/oc
ENTRYPOINT ["/usr/bin/multi-arch-builder-controller"]
