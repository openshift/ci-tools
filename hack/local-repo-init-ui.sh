#!/bin/bash

set -euo pipefail


function showHelp() {
  echo "You must run either ./local-repo-init-ui.sh start | stop"
}

function start() {
  cd $(dirname $0)/../cmd/repo-init/frontend

  echo "Building UI"

  npm run build

  cd ..

  go install -gcflags="all=-N -l"

  repo-init \
    --loglevel=debug \
    --mode=api \
    --port=8080 \
    --github-endpoint=https://api.github.com \
    --disable-cors=true \
    --num-repos=1 \
    --server-config-path=/tmp/serverconfig &

  repo-init \
    --loglevel=debug \
    --mode=ui \
    --port=9000 \
    --metrics-port=9001 \
    --health-port=9002 &

  echo "Running on http://127.0.0.1:9000"
}

function stop() {
  rm -rf ${TMPDIR:-"/tmp"}/repo-manager*
  pkill -f repo-init
}


if [ $# -eq 0 ]; then
  showHelp
  exit 1
fi

if [ "$1" = "start" ]; then
  start
elif [ "$1" = "stop" ]; then
  stop
else
  echo "ERROR: invalid command specified"
  showHelp
fi
