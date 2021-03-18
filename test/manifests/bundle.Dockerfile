FROM scratch

LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=metering-ocp
LABEL operators.operatorframework.io.bundle.channels.v1=test-channel
LABEL operators.operatorframework.io.bundle.channel.default.v1=test-channel

COPY 4.6/*.yaml /manifests/
COPY metadata /metadata/
