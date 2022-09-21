#!/bin/sh
set -e
secret_path=/etc/pull-secret/.dockerconfigjson
export REGISTRY_PROXY_USERNAME=$(cat $secret_path | jq '.auths["quay.io"].auth' -r | base64 -d | cut -d ':'  -f 1)
export REGISTRY_PROXY_PASSWORD=$(cat $secret_path | jq '.auths["quay.io"].auth' -r | base64 -d | cut -d ':'  -f 2)
htpasswd -Bbn ${REGISTRY_PROXY_USERNAME} ${REGISTRY_PROXY_PASSWORD} > /tmp/htpasswd
config="/etc/docker/registry/config.yml"
registry serve $config &
pid=$!
while true; do
    quay_io_username=$(cat $secret_path | jq '.auths["quay.io"].auth' -r | base64 -d | cut -d ':'  -f 1)
    quay_io_password=$(cat $secret_path | jq '.auths["quay.io"].auth' -r | base64 -d | cut -d ':'  -f 2)
    if [ "$REGISTRY_PROXY_USERNAME" != "$quay_io_username" ] || [ "$REGISTRY_PROXY_PASSWORD" != "$quay_io_password" ]; then
        kill $pid
        break
    fi
    sleep 30
done
