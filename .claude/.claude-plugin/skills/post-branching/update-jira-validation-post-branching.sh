#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current> <x.y+2> <release-repo>" && exit 1

XY_PLUS_2="$2"
XY_PLUS_3=$(echo "$XY_PLUS_2" | awk -F. '{printf "%d.%d", $1, $2+1}')
XY_PLUS_1=$(echo "$XY_PLUS_2" | awk -F. '{printf "%d.%d", $1, $2-1}')
JIRA="$3/core-services/jira-lifecycle-plugin/config.yaml"

[ ! -f "$JIRA" ] && echo "Error: Jira config not found" && exit 1
grep -q "openshift-${XY_PLUS_2}:" "$JIRA" && echo "Warning: Already exists, skipping" && exit 0

OPENSHIFT_STANZA="  openshift-${XY_PLUS_2}:
    dependent_bug_states:
    - status: MODIFIED
    - status: ON_QA
    - status: VERIFIED
    dependent_bug_target_versions:
    - ${XY_PLUS_3}.0
    target_version: ${XY_PLUS_2}.0
    validate_by_default: true"

RELEASE_STANZA="  release-${XY_PLUS_2}:
    dependent_bug_states:
    - status: MODIFIED
    - status: ON_QA
    - status: VERIFIED
    dependent_bug_target_versions:
    - ${XY_PLUS_3}.0
    target_version: ${XY_PLUS_2}.0
    validate_by_default: true"

awk -v new_stanza="$OPENSHIFT_STANZA" -v prev="openshift-${XY_PLUS_1}:" '
{
    if ($0 ~ prev) { in_stanza = 1 }
    else if (in_stanza && /^  [a-z]/ && !/^    /) { print new_stanza; in_stanza = 0 }
    print
}' "$JIRA" > "${JIRA}.tmp"

awk -v new_stanza="$RELEASE_STANZA" -v prev="release-${XY_PLUS_1}:" '
{
    if ($0 ~ prev) { in_stanza = 1 }
    else if (in_stanza && (/^  [a-z]/ && !/^    / || /^[a-z]/)) { print new_stanza; in_stanza = 0 }
    print
}' "${JIRA}.tmp" > "${JIRA}.tmp2"

mv "${JIRA}.tmp2" "$JIRA"
rm -f "${JIRA}.tmp"

echo "✓ Jira validation: openshift-$XY_PLUS_2, release-$XY_PLUS_2"
