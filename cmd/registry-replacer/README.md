# Registry replacer

A small utility used to make sure that all builds use a cluster-local registry. It:

* Finds all ci-operator configs with at least one images directive
* Downloads the corresponding Dockerfile
* If it has a reference to the api.ci registry, updates the ci-operator config to replace that with a `base_image`
* If it has replacements, checks if those apply and if not, removes them
* Removes all replacements for `ocp/builder` images
* Updates the `Dockerfile` in the images config to match whats defined in the ocp-build-data repository
