#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <x.y+1> <x.y+2> <release-repo>" && exit 1

CURRENT="$1"
NEXT="$2"
CI_TOOLS=$(pwd)
RELEASE=$(cd "$3" && pwd)

[ ! -d "$RELEASE" ] && echo "Error: Release repo not found" && exit 1

cd "$CI_TOOLS"
for tool in config-brancher ci-operator-config-mirror rpm-deps-mirroring-services; do
    command -v $tool &>/dev/null || go install ./cmd/$tool
done

cd "$RELEASE"
git fetch upstream && git reset --hard upstream/master

config-brancher --config-dir ci-operator/config --current-release=$CURRENT --future-release=$NEXT --confirm
git add ci-operator/config && git commit -m "[$NEXT] Generate ci-operator configuration"

ci-operator-config-mirror --config-dir ./ci-operator/config --to-org openshift-priv --only-org openshift --whitelist-file ./core-services/openshift-priv/_whitelist.yaml
git add ci-operator/config && git commit -m "[$NEXT] Generate ci-operator configuration for openshift-priv"

make jobs
git add ci-operator/jobs && git commit -m "[$NEXT] Generate vanilla jobs for $NEXT"

rpm-deps-mirroring-services --current-release "$CURRENT" --release-repo "$RELEASE"
git add . && git commit -m "[$NEXT] rpm dependency update"

cat <<'EOF' >/tmp/filter.py
#!/usr/bin/env python3
import yaml, sys
with open(sys.argv[1]) as f:
    all = yaml.full_load(f)
    for t in ("presubmits", "postsubmits"):
        for repo in all.get(t, {}):
            all[t][repo] = [j for j in all.get(t, {}).get(repo, []) if j.get("agent", "") == "kubernetes"]
with open(sys.argv[1], 'w') as f:
    yaml.dump(all, f, default_flow_style=False)
EOF
chmod +x /tmp/filter.py

find ci-operator/jobs/ -name '*-release-'$CURRENT'-*submits.yaml' -or -name '*-'$CURRENT'-periodics.yaml' | while read item; do
    for main in master main; do
        [ -e "${item/release-$CURRENT/$main}" ] || continue
        cp "${item/release-$CURRENT/$main}" "${item/$CURRENT/$NEXT}"
        sed -i "s/-$main-/-release-$NEXT-/g; s/- \^$main/- \^release-$NEXT/g" "${item/$CURRENT/$NEXT}"
        /tmp/filter.py "${item/$CURRENT/$NEXT}"
        break
    done
done

rm -f ci-operator/jobs/openshift/release/openshift-release-release-$NEXT-periodics.yaml
make jobs
git add ci-operator/jobs && git commit -m "[$NEXT] Carry job customization over from master/main jobs"

echo "✓ CI configs + jobs: $CURRENT → $NEXT"
git log --oneline origin/master..HEAD
