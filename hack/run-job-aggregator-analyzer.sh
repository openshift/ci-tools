#!/usr/bin/env bash

set -euo pipefail

## Run this script to locally analyze data for disruption.
## This helper script allows you the ability to compare against local data or directly pulling all data from 
## Big Query using provided GC Credentials.
##
## Because a local file will be treated as authoritative just as if we were querying against Big Query it can provide a way
## to run a much more refined query via the Big Query UI, downloading the results and feeding it through.
##
## In the event where you can't wait for the automation to run, or you need to quickly update, you can just provide a GC Token locally
## and this script will generate the `pr_message.md` to provide in your PR as well as the results.

ORIGIN_DIR=""
ALERTS_FILE=""
DISRUPTION_FILE=""
DISRUPTION_FILE=""
GC_CREDS=""
UPDATE=""
TEMP_DIR=""

function print_usage() {
    printf "Usage:
        -o | Required: root directory location for openshift/origin
        -a | Required: local alerts file to compare against
        -d | Required: local disruption file to compare against
        -g | Optional: google auth file to query ci data from
        -u | Optional: copy output files back into openshift/origin
"
}

function cleanup() {
    rm -rf $TEMP_DIR
}

while getopts "o:a:d:g:u" f; do
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
    g)
        GC_CREDS=${OPTARG}
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

if [ -z "$ORIGIN_DIR" ]; then
        print_usage
        exit 1
fi

if ([ -z "$ALERTS_FILE" ] || [ -z "$DISRUPTION_FILE" ]) && [ -z "$GC_CREDS" ] ; then
        print_usage
        exit 1
fi

echo "Building job-run-aggregator"
go build -gcflags='-N -l' `grep "module " go.mod |awk '{print $2}'`/cmd/job-run-aggregator

TEMP_DIR=$(mktemp -d)
echo "Created temp dir $TEMP_DIR"
echo "Copying query data"
cp "$ORIGIN_DIR/pkg/monitortestlibrary/allowedalerts/query_results.json" "$TEMP_DIR/current-alerts.json"
cp "$ORIGIN_DIR/pkg/monitortestlibrary/allowedbackenddisruption/query_results.json" "$TEMP_DIR/current-disruptions.json"


if ! [ -z "$GC_CREDS" ]; then
    ./job-run-aggregator analyze-historical-data  \
        --current $TEMP_DIR/current-alerts.json \
        --data-type alerts \
        --leeway 30 \
        --google-service-account-credential-file $GC_CREDS

    ./job-run-aggregator analyze-historical-data  \
        --current $TEMP_DIR/current-disruptions.json \
        --data-type disruptions \
        --leeway 30 \
        --google-service-account-credential-file $GC_CREDS
else
    ./job-run-aggregator analyze-historical-data  \
        --current $TEMP_DIR/current-alerts.json \
        --data-type alerts \
        --new $ALERTS_FILE \
        --leeway 30

    ./job-run-aggregator analyze-historical-data  \
        --current $TEMP_DIR/current-disruptions.json \
        --data-type disruptions \
        --new $DISRUPTION_FILE \
        --leeway 30
fi

cleanup

if ! [ -z "$UPDATE" ]; then
    cp ./results_disruptions.json "$ORIGIN_DIR/pkg/monitortestlibrary/allowedbackenddisruption/query_results.json"
    cp ./results_alerts.json "$ORIGIN_DIR/pkg/monitortestlibrary/allowedalerts/query_results.json"
fi
