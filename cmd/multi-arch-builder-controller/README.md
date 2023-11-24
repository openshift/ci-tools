# multi-arch-builder-controller

The controller reconciles the MultiArchBuildConfig and generates multiple builds one for each architecture that exists on the cluster. Once all builds succeed, the controller uses the manifest-tool binary to create a new image based on the output configuration that includes the manifest list with all images that have been built per architecture correspondingly.


```console
$ ./multi-arch-builder-controller --help
Usage of ./multi-arch-builder-controller:
  -dry-run
    	Whether to run the controller-manager with dry-run (default true)
```


## Requirements

- `manifest-tool` binary included in the container image
- target registry credentials mounted on /.docker/config.json 