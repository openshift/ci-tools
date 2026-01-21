#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <x.y+1> <x.y+2> <release-repo>" && exit 1

XY_PLUS_1="$1"
XY_PLUS_2="$2"
CONFIG="$3/core-services/image-mirroring/openshift/_config.yaml"

[ ! -f "$CONFIG" ] && echo "Error: Image mirroring config not found" && exit 1

add_version() {
    local prefix="$1" after="$2" new="$3"
    local after_key="${prefix}${after}" new_key="${prefix}${new}"

    grep -q "\"${new_key}\":" "$CONFIG" && return 0

    awk -v after_key="$after_key" -v new_key="$new_key" -v new_ver="$new" '
    /^  "'"$after_key"'":/ { in_section = 1 }
    in_section && /^  "/ && !/^  "'"$after_key"'":/ {
        print "  \"" new_key "\":"
        print "  - \"" new_ver "\""
        print "  - \"" new_ver ".0\""
        in_section = 0
    }
    { print }
    ' "$CONFIG" > "${CONFIG}.tmp"

    mv "${CONFIG}.tmp" "$CONFIG"
}

for prefix in "" "sriov-" "metallb-" "ptp-" "scos-"; do
    add_version "$prefix" "$XY_PLUS_1" "$XY_PLUS_2"
done

echo "✓ image-mirroring: $XY_PLUS_1 → $XY_PLUS_2 (all variants)"
