# ci-images-mirror

This tool mirrors images used by tests in OpenShift CI from `app.ci`'s integrated registry to `quay.io/openshift/ci`. It is a temporary automation while migrating all users of the CI images
from `app.ci` to `quay.io`. When the migration process is complete, i.e., all clients uses images from `quay.io`,
this tool will be decommissioned and CI images will stop promoting images to `app.ci`.

The tool watches all image stream tags on `app.ci`, and compare its digest with the one from the targeting image on `quay.io`.
If they have different digests or the target image does not exist, a mirroring task will be created in a queue which will
be picked up and executed afterwords.

Similar to the `ci-operator's` promotion, there are always 2 tags to push to keep all the images from quay.io's image pruning:
For example, `registry.ci.openshift.org/namespace/name:tag` is mirrored to
- `quay.io/openshift/ci:name_tag`, and
- `quay.io/openshift/ci:<Date>_sha256_<DIGEST>` where `<Date>` is today's date, e.g., `20231029` and `<DIGEST>` is the
  SHA256 hash of the docker image without the prefix `sha256:`.

This tool is extended with the following features (and thus is no longer temporary):
- mirror images from external registries to QCI: See `.supplementalCIImages` of [the configuration file](https://github.com/openshift/release/blob/main/core-services/image-mirroring/_config.yaml).
- mirror ART images from app.ci to QCI: See `.artImages` of [the configuration file](https://github.com/openshift/release/blob/main/core-services/image-mirroring/_config.yaml).

## Run the tool locally

```console
# E.g., RELEASE="/Users/hongkliu/repo/openshift/release"
$ RELEASE=<PATH_TO_THE_RELEASE_REPO> hack/run-ci-images-mirror.sh

# Check the tasks in the queue
$ curl -s http://localhost:8090/api/v1/mirrors\?action\=show\&limit\=1 | jq
{
  "mirrors": [
    {
      "source_tag_ref": {
        "namespace": "ocp-private",
        "name": "4.9-priv",
        "tag": "telemeter"
      },
      "source": "registry.ci.openshift.org/ocp-private/4.9-priv@sha256:censored_digest",
      "destination": "quay.io/openshift/ci:ocp-private_4.9-priv_telemeter",
      "current_quay_digest": "",
      "created_at": "2023-10-11T10:15:13.179246-04:00",
      "stale": true
    }
  ],
  "total": 14
}

# grep mirror cmd in the log
$ RELEASE="/Users/hongkliu/repo/openshift/release" hack/run-ci-images-mirror.sh 2>&1 | grep 'image mirror'
```

## Debug the deployment in production

```console
$ oc port-forward -n ci $(oc get pods -n ci -l app=ci-images-mirror --no-headers -o custom-columns=":metadata.name") 28090:8090

$ curl -s http://localhost:28090/api/v1/mirrors\?action\=show\&limit\=1  | jq
{
  "mirrors": [
    {
      "source_tag_ref": {
        "namespace": "ocp-private",
        "name": "4.9-priv",
        "tag": "node-problem-detector"
      },
      "source": "registry.ci.openshift.org/ocp-private/4.9-priv@sha256:censored_digest",
      "destination": "quay.io/openshift/ci:20231011_sha256_censored_digest",
      "current_quay_digest": "",
      "created_at": "2023-10-11T14:46:23.426063734Z",
      "stale": true
    }
  ],
  "total": 12
}

$ oc logs -n ci -l app=ci-images-mirror -c ci-images-mirror -f | grep -i keep-manifest-list
```