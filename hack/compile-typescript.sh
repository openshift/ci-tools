#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

if [[ -n "${SKIPPED_IMAGES:-}" ]] && echo "${SKIPPED_IMAGES}" | grep -q "vault-secret-collection-manager"; then
	exit 0
fi

if ! command -V tsc >/dev/null 2>&1; then
	if [[ -n "${CI:-}" ]]; then
		echo "[FATAL] In the CI environment, the TypeScript compiler is required!"
		exit 1
	fi
	echo "[WARNING] No TypeScript compiler found, using dummy data for Vault collection manager front-end."
	touch cmd/vault-secret-collection-manager/index.js
	exit 0
fi

tsc --lib ES2016,dom cmd/vault-secret-collection-manager/index.ts
