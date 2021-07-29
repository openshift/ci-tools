#!/bin/bash
tmpdir="/tmp"

#There are no prometheus images for darwin so we must fake it for now
prometheusdir="$tmpdir/prometheus"
if [ ! -d $prometheusdir ]
then
  echo "creating empty $prometheusdir"
  mkdir $prometheusdir
fi

#There are no promtool images for darwin so we must fake it for now
promtooldir="$tmpdir/promtool"
if [ ! -d $promtooldir ]
then
  echo "creating empty $promtooldir"
  mkdir $promtooldir
fi

echo "Running on context: "
oc config current-context

TMPDIR=$tmpdir \
ARTIFACT_DIR=/tmp/artifacts \
make local-e2e \
TESTFLAGS="-run $1" \
GOTESTSUM_FORMAT=standard-verbose
