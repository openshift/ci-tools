prowgen
=======

Prowgen is a tool that converts [ci-operator configuration](https://docs.ci.openshift.org/docs/architecture/ci-operator/)
into [Prowjobs](https://docs.prow.k8s.io/docs/jobs/).

The objetive is to avoid tailored `Prowjobs` and made `ci-operator configuration` the only
source of truth for jobs created based on configurations inside `openshift/ci-operator/config/`.

Prowgen is normally executed by `make update/make jobs` inside `openshift/release` folder. 

Testing
-------

`Prowgen` is hardcoded to use `GOPATH` + `src/github.com/openshift/release`, if you want to test it on your machine
you can use a symbolic link ponting to your `openshift/release` clone:

```bash
# generally GOPATH=~/go
ln -s ~/cloned-repos/openshift/release ~/go/src/github.com/openshift/release
```

Then you can execute `ci-operator-prowgen`:

```bash
ci-operator-prowgen \
    --from-release-repo \
    --to-release-repo \
    --known-infra-file infra-build-farm-periodics.yaml \
    --known-infra-file infra-periodics.yaml \
    --known-infra-file infra-image-mirroring.yaml \
    --known-infra-file infra-periodics-origin-release-images.yaml
```
