#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

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

if [[ "${1}" != "${test}" ]]; then
	# we saw a change to an unrelated secret, nothing to do
	exit 0
fi

raw="$( oc get secrets "${test}" --namespace "${namespace}" -o jsonpath="{.data.kubeconfig}" )"
if [[ -n "${raw}" ]]; then
	echo -n "${kubeconfig}" | base64 --decode > "${output}/kubeconfig.yaml"
	echo "\$KUBECONFIG saved to ${output}/kubeconfig.yaml"
	exit 0
fi
echo "No \$KUBECONFIG for the $test test has been created yet, waiting..."
EOF
chmod +x "${output}/extract.sh"

oc --namespace "${namespace}" observe secrets -- "${output}/extract.sh" &

while true; do
	if [[ -s "${output}/kubeconfig.yaml" ]]; then
		exit 0
	fi
	sleep 1
done

for job in $( jobs -p ); do
	kill -SIGTERM "${job}"
	wait "${job}"
done
