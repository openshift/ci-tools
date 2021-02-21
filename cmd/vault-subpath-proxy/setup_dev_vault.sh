#!/bin/bash

set -exuo pipefail

vault server -dev --dev-listen-address=127.0.0.1:8300 -dev-root-token-id=jpuxZFWWFW7vM882GGX2aWOE &

trap 'kill $(ss -ltpn|grep 8300|cut -d '=' -f2|cut -d, -f1)' EXIT

until curl --fail localhost:8300/v1/sys/health; do
  echo "Vault not ready yet"
  sleep 1s
done

export VAULT_ADDR=http://127.0.0.1:8300
export VAULT_TOKEN=jpuxZFWWFW7vM882GGX2aWOE

# Make the mount visible to autheticated users
vault secrets tune -listing-visibility=unauth secret

vault policy write team-1 -<<EOH
path "secret/data/team-1/*" {
  capabilities = ["create", "update", "read", "delete"]
}

path "secret/metadata/team-1/*" {
  capabilities = ["list"]
}
EOH

vault kv put secret/top-level key=value
vault kv put secret/team-1/team-1-secret key=value

echo "Creating token that can only see team-1 data in kv store"
vault token create -policy=team-1 -policy=default

wait $(jobs -p)
