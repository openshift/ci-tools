#!/bin/bash
# Skill: download-sippy-config
# Description: Download latest Sippy configuration from GitHub
# Usage: download-sippy-config.sh [output_path]

set -euo pipefail

OUTPUT_PATH="${1:-/tmp/sippy-openshift.yaml}"

echo "=========================================="
echo "Downloading Sippy Configuration"
echo "=========================================="
echo ""

echo "Downloading from: https://raw.githubusercontent.com/openshift/sippy/master/config/openshift.yaml"
echo "Output path: ${OUTPUT_PATH}"

curl -sSL -o "${OUTPUT_PATH}" https://raw.githubusercontent.com/openshift/sippy/master/config/openshift.yaml

if [ -f "${OUTPUT_PATH}" ]; then
    echo ""
    echo "=========================================="
    echo "✓ Downloaded successfully to ${OUTPUT_PATH}"
    echo "=========================================="
else
    echo ""
    echo "=========================================="
    echo "✗ Download failed!"
    echo "=========================================="
    exit 1
fi
