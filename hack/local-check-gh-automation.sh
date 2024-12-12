#!/bin/bash

source "$(dirname "${BASH_SOURCE}")/lib/init.sh"

TEMP=$(getopt --options "p:" --longoptions "candidate-path:" -n $0 -- "$@")
eval set -- "$TEMP"

while true; do
  case "$1" in
    -p | --candidate-path)
      candidate_path="$2"
      shift 2
      ;;
    --)
      shift
      break
      ;;
    *)
      echo 'Internal error' >&2
      exit 1
      ;;
  esac
done

function OC() {
	oc --context app.ci --namespace ci "$@"
}

function cleanup() {
  return_code=$?
  os::cleanup::all
  os::util::describe_return_code "${return_code}"
  exit "${return_code}"
}
trap "cleanup" EXIT

data="${BASETMPDIR}/data"
mkdir -p "${data}"
mkdir -p "${data}/prow"

plugin_dir="${data}/plugins"
mkdir -p "${plugin_dir}"


os::log::info "Extracting production data we need to run check-gh-automation..."
OC extract secret/openshift-prow-github-app --keys appid,cert --to "${data}"
OC extract configmap/plugins --to "${plugin_dir}"
OC extract configmap/config --to "${data}/prow"

app_id=$(cat "${data}/appid")

os::log::info "Running check-gh-automation"
go run ./cmd/check-gh-automation --repo="$1" \
 ${candidate_path:+--candidate-path=$candidate_path} \
  --bot=openshift-merge-robot --bot=openshift-ci-robot \
  --github-app-id="$app_id" --github-app-private-key-path="${data}/cert" \
  --config-path="${data}/prow/config.yaml" --supplemental-prow-config-dir="${data}/prow" \
  --plugin-config="${data}/plugins/plugins.yaml" --supplemental-plugin-config-dir="${data}/plugins" \
