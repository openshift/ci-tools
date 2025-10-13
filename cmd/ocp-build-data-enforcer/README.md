# OCP Build data enforcer

This tool:

* Iterates over the content in https://github.com/openshift/ocp-build-data/tree/openshift-4.21/images for all openshift versions
* Downloads the Dockerfile specified in `content.source.Dockerfile` (Default: `Dockerfile`)
* Checks if it `From` directive matches the build-cluster equivalent of the de-referenced `from.stream`
* If not, updates it and creates a Pull Request
