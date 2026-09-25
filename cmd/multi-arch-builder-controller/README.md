# multi-arch-builder-controller

The controller reconciles the MultiArchBuildConfig and generates multiple builds one for each architecture that exists on the cluster. Once all builds succeed, the controller uses the manifest-tool binary to create a new image based on the output configuration that includes the manifest list with all images that have been built per architecture correspondingly.

When `external_registries` is set and `build_spec.output.to.namespace` differs from the MABC namespace, the controller writes the architecture images and manifest to a build image in the MABC namespace. The requested output namespace is used to name the external mirror destination; the controller does not create an ImageStreamTag there. For example, a MABC in `ci` with output `ocp/cli-yq:latest` and external registry `quay.io/openshift/ci` uses build image `ci/ocp-cli-yq:latest` and mirrors it to `quay.io/openshift/ci:ocp_cli-yq_latest`.

```console
$ ./multi-arch-builder-controller --help
Usage of ./multi-arch-builder-controller:
  -dry-run
    	Whether to run the controller-manager with dry-run (default true)
```


## Requirements

- `manifest-tool` binary included in the container image
- target registry credentials mounted on /.docker/config.json
