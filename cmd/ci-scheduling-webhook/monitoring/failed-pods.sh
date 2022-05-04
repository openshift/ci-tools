#!/bin/bash

echo "Failed pods:"
oc get pods --all-namespaces -o=wide --field-selector=status.phase==Failed
