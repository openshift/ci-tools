prowgen
=======

Prowgen is a tool that generates [job configurations](https://docs.prow.k8s.io/docs/jobs/) based on
[ci-operator configuration](https://docs.ci.openshift.org/docs/architecture/ci-operator/) and its own
configuration file named `.config.prowgen`.

The contents of `.config.prowgen` will be appended to every job configuration during Prowgen execution:

**Example:**

```yaml
slack_reporter:
- channel: "#ops-testplatform"
  job_states_to_report:
  - failure
  - error
  report_template: ':failed: Job *{{.Spec.Job}}* ended with *{{.Status.State}}*. <{{.Status.URL}}|View logs> {{end}}'
  job_names:
  - images
```

Most of the time, Prowgen will overwrite configurations on `openshift/ci-operator/jobs/` with the ones
defined in `openshift/ci-operator/jobs/`.

Prowgen is tipically run using `make update` or `make jobs` from within `openshift/release` directoy. 

Testing
-------

`Prowgen` is hardcoded to use `GOPATH` + `src/github.com/openshift/release`, if you want to
test it on your machine you can run the tool directly from the `openshift/release` repository
root path or use a symbolic link ponting to your `openshift/release` clone:

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
