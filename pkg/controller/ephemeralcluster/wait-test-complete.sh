#!/bin/bash

# This loop keeps the ephemeral cluster up and running and then waits for
# a konflux test to complete. Once the test is done, the EphemeralCluster 
# controller creates a synthetic secret 'test-done-keep-going' into this ci-operator NS,
# unbloking the workflow and starting the deprovisioning procedures.

i=0
while true ; do
    printf 'attempt %d\n' $i
    if $(oc get secret/test-done-keep-going 2>&1 | grep -qv 'not found'); then
        break
    fi
    i=$((i+1))
    sleep 5s
done
