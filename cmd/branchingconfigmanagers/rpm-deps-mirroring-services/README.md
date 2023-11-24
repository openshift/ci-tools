# RPM dependencies mirroring services
This manager attempts to automatize step (1.) of "[Few weeks before branching day](https://docs.google.com/document/d/1Z6ejnDCOCvNv9PWkyNPzVbjuLbDMAAT5GEeDpzb0SMs/edit#heading=h.r9xn02r1cyfn)" phase.

## Usage
### Options:
- `--current-release` specifies the current OCP version
- `--release-repo` is the absolute path to `openshift/release` repository

### Example
```sh
    $ ./generated-release-gating-jobs \
        --current-release "4.12" \
        --release-repo "/full/path/to/openshift/release/repo"
```