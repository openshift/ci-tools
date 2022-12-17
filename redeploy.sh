#!/bin/bash

set -e

tag="buildfarm-$(oc whoami --show-server | md5sum | cut -d ' ' -f 1)"

CGO_ENABLED=0 go build -ldflags="-extldflags=-static" github.com/openshift/ci-tools/cmd/ci-scheduling-webhook
sudo docker build -t quay.io/jupierce/ci-scheduling-webhook:${tag} -f images/ci-scheduling-webhook/Dockerfile .
sudo docker push quay.io/jupierce/ci-scheduling-webhook:${tag}
cat cmd/ci-scheduling-webhook/res/deployment.yaml | sed 's/latest/'${tag}'/' | oc apply --as system:admin -f -
oc --as system:admin -n ci-scheduling-webhook delete pods --all
sleep 1
oc get pods -n ci-scheduling-webhook
