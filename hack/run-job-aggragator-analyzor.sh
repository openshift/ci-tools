#!/usr/bin/env bash

set -euo pipefail

## Run this script to locally analyze data for disruption

ORIGIN_DIR=""
ALERTS_FILE=""
DISRUPTION_FILE=""
DISRUPTION_FILE=""
UPDATE=""
TEMP_DIR=""

function print_usage() {
    printf "Usage:
        -o | Required: root directory location for openshift/origin
        -a | Required: local alerts file to compare against
        -d | Required: local disruption file to compare against
        -u | Optional: copy output files back into openshift/origin
"
}

function cleanup() {
    rm -rf $TEMP_DIR
}

while getopts "o:a:d:u" f; do
    case "$f" in
    o)
        ORIGIN_DIR=${OPTARG}
        ;;
    a)
        ALERTS_FILE=${OPTARG}
        ;;
    d)
        DISRUPTION_FILE=${OPTARG}
        ;;
    u)
        UPDATE="true"
        ;;
    *)
        print_usage
        exit 1
        ;;
    esac
done

if [ -z "$ORIGIN_DIR" ] || [ -z "$ALERTS_FILE" ] || [ -z "$DISRUPTION_FILE" ]; then
        print_usage
        exit 1
fi

echo "Building job-run-aggregator"
go build -gcflags='-N -l' `grep "module " go.mod |awk '{print $2}'`/cmd/job-run-aggregator

TEMP_DIR=$(mktemp -d)
echo "Created temp dir $TEMP_DIR"
echo "Copying query data"
cp "$ORIGIN_DIR/pkg/synthetictests/allowedalerts/query_results.json" "$TEMP_DIR/current-alerts.json"
cp "$ORIGIN_DIR/pkg/synthetictests/allowedbackenddisruption/query_results.json" "$TEMP_DIR/current-disruptions.json"


./job-run-aggregator analyze-historical-data  \
    --current $TEMP_DIR/current-alerts.json \
    --data-type alerts \
    --new $ALERTS_FILE \
    --leeway 1m

./job-run-aggregator analyze-historical-data  \
    --current $TEMP_DIR/current-disruptions.json \
    --data-type disruptions \
    --new $DISRUPTION_FILE \
    --leeway 1m

cleanup

if ! [ -z "$UPDATE" ]; then
    cp ./results_disruptions.json $ORIGIN_DIR/pkg/synthetictests/allowedbackenddisruption/query_results.json
    cp ./results_alerts.json "$ORIGIN_DIR/pkg/synthetictests/allowedalerts/query_results.json"
fi
