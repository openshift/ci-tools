# multi-arch-builder-controller

The controller reconciles the MultiArchBuildConfig and generates multiple builds one for each architecture that exists on the cluster. Once all builds succeed, the controller uses the manifest-tool binary to create a new image based on the output configuration that includes the manifest list with all images that have been built per architecture correspondingly.

With `build_spec.output.to.kind: DockerImage`, the builds run in the MABC namespace and push architecture tags directly to the configured registry. The controller then pushes the multi-architecture manifest to the configured image tag. For example, output `quay.io/openshift/ci:ocp_cli-yq_latest` produces `-amd64` and `-arm64` tags, then the manifest at the original tag. This mode does not need an ImageStream or `external_registries`. The build's `output.pushSecret` and the controller's registry config must both authorize pushes to the registry.

```console
$ ./multi-arch-builder-controller --help
Usage of ./multi-arch-builder-controller:
  -dry-run
    	Whether to run the controller-manager with dry-run (default true)
```


## Requirements

- `manifest-tool` binary included in the container image
- target registry credentials mounted on /.docker/config.json
