FROM mplatform/manifest-tool as builder
FROM quay.io/centos/centos:stream8

COPY --from=builder /manifest-tool /usr/bin/manifest-tool

ADD multi-arch-builder-controller /usr/bin/multi-arch-builder-controller
ENTRYPOINT ["/usr/bin/multi-arch-builder-controller"]
