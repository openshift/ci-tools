#!/usr/bin/env bash

set -euxo pipefail

tmp_file=$(mktemp)
trap 'rm -rf $tmp_file' EXIT

set +e
go-errorlint -errorf ./... 2>&1 |grep 'non-wrapping format verb for fmt.Errorf.' >$tmp_file
set -e

cd $(dirname $0)/..
while read failure; do
  file="$(echo $failure|cut -d: -f1)"
  line="$(echo $failure|cut -d: -f2)"
  sed -i "${line}s/%v/%w/" $file
done <$tmp_file
