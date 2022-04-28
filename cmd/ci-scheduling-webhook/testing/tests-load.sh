#!/bin/sh

BASEDIR=$(dirname "$0")

oc delete --as system:admin -n ci-op-jmp pods -l ci-workloads=tests

for i in 1 2 3 4 5 6 7 8 9 10; do
  echo "Creating pod ${i}"
  cat ${BASEDIR}/example_tests_pod.yaml | sed 's/webhook-example-test-pod-busybox/webhook-example-test-pod-busybox-'${i}-$(date "+%s")'/' | sed 's/100m/3000m/' | oc apply --as system:admin -f -
done