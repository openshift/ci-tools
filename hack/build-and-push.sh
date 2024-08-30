TOOL=$1
QUAY_ACCOUNT=$2
TMPDIR=${TMPDIR:-/tmp}
CONTAINER_ENGINE=${CONTAINER_ENGINE:-podman}

echo "Building linux binary of ${TOOL}"
GOOS=linux GOARCH=amd64 go build -v -o "${TMPDIR}/bin/linux/${TOOL}" "./cmd/${TOOL}"
echo "Building ${CONTAINER_ENGINE} image quay.io/${QUAY_ACCOUNT}/${TOOL}:latest"
if "${CONTAINER_ENGINE}" build --platform linux/amd64 -f "images/${TOOL}/Dockerfile" "${TMPDIR}/bin/linux" -t "quay.io/${QUAY_ACCOUNT}/${TOOL}:latest"; then
  echo build succeeded
else
  echo "build failed; make sure the images/${TOOL} DOCKERFILE exists"
fi
echo "Pushing ${CONTAINER_ENGINE} image quay.io/${QUAY_ACCOUNT}/${TOOL}:latest"
"${CONTAINER_ENGINE}" push "quay.io/${QUAY_ACCOUNT}/${TOOL}:latest"
