#!/bin/sh

BASEDIR=$(dirname "$0")

#echo "Patches for tests workload"
#curl -k -H "Content-Type: application/json" -d @${BASEDIR}/tests-admission-review-request.json -X POST https://127.0.0.1:8443/mutate | jq -r .response.patch | base64 -d | jq .

#echo "Patches for builds workload"
curl -k -H "Content-Type: application/json" -d @${BASEDIR}/builds-admission-review-request.json -X POST https://127.0.0.1:8443/mutate | jq -r .response.patch | base64 -d | jq .