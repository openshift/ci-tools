#!/bin/bash

echo "Unschedulable pods:"
oc get pods --all-namespaces -o=wide --field-selector=status.phase==Pending
