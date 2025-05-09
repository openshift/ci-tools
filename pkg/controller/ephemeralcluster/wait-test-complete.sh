#!/bin/bash

# This loop keeps the ephemeral cluster up and running and then waits for
# a konflux test to complete. Once the test is done, the EphemeralCluster 
# controller creates a synthetic secret 'test-done-signal' into this ci-operator NS,
# unbloking the workflow and starting the deprovisioning procedures.

# This kubeconfig points to the ephemeral cluster. Unsetting it as we want to reach out to
# the build farm cluster.
unset KUBECONFIG

i=0
unexpected_err=0
secret='test-done-signal'

while true; do
    printf 'attempt %d\n' $i

    output="$(oc get secret/$secret 2>&1)"
    if [ $? -eq 0 ]; then
        printf 'secret found\n'
        break
    fi

    # The sole error we expect to hit is 'not found'. Break the loop if we collect
    # this many unexpected errors in a row.
    if ! $(grep -qF "secrets \"$secret\" not found" <<<"$output"); then
        printf 'unexpected error: %d\n' $unexpected_err

        if [ $unexpected_err -ge 3 ]; then
            printf 'FAILURE: too many unexpected errors\n' $unexpected_err
            break
        fi

        unexpected_err=$((unexpected_err+1))
    else
        unexpected_err=0
    fi

    i=$((i+1))
    sleep 5s
done
