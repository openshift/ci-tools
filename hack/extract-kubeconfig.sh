#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

function cleanup() {
	for job in $( jobs -p ); do
		kill -SIGTERM "${job}"
		wait "${job}"
	done
}

trap cleanup EXIT

namespace="${1:-}"
test="${2:-}"
if [[ -z "${namespace}" || -z "${test}" ]]; then
	echo "USAGE: $0 <namespace> <test>"
	exit 1
fi

echo "Scanning ${namespace} for the \$KUBECONFIG for the ${test} test..."
output="$( mktemp -d /tmp/kubeconfig.XXXXX )"
cat <<EOF >"${output}/extract.sh"
#!/bin/bash

if [[ "\${2}" != "${test}" ]]; then
	# we saw a change to an unrelated secret, nothing to do
	exit 0
fi

oc extract --namespace "${namespace}" "secret/${test}" --keys=kubeconfig --to="${output}" >/dev/null
if [[ -s "${output}/kubeconfig" ]]; then
	exit 0
fi
echo "No \\\$KUBECONFIG for the ${test} test has been created yet, waiting..." 1>&2
EOF
chmod +x "${output}/extract.sh"

if ! oc --namespace "${namespace}" get secrets >/dev/null; then
	echo "You do not have permissions to see secrets for this namespace. Did you enter the namespace correctly?"
	exit 1
fi
oc --namespace "${namespace}" observe secrets --no-headers -- "${output}/extract.sh" &

while true; do
	if [[ -s "${output}/kubeconfig" ]]; then
		echo "\$KUBECONFIG saved to ${output}/kubeconfig"
		exit 0
	fi
	sleep 1
done
