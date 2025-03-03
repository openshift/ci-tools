pj-rehearse
===========

The `pj-rehearse` is a plugin for [Prow](https://docs.prow.k8s.io/docs/overview/), it indentify modifications that will affect
other jobs and execute that jobs with the modifications without merging it.

After each pull request, `pj-rehearse` will search for affected jobs and
display a list of jobs that can be rehearsed. We can rehearse as many as we
want but preferably choose only a few of them.

Jobs are expected to have a label `pj-rehearse ack`, this label should be
automatically placed if no jobs are affected by the modifications, after
`/pj-rehearse ack` or upon `/pj-rehearse auto-ack` success.

The following ports are available after `pj-rehearse` is running:

- **6060** - Profiling.
- **8081** - Health check.
- **9090** - Prometheus metrics.
- **8888** - Application HTTP endpoint.

Commands
--------

The following commands are supported:

- `/pj-rehearse` - to run up to 5 rehearsals
- `/pj-rehearse skip` - to opt-out of rehearsals
- `/pj-rehearse {test-name}` - with each test separated by a space, to run one or more specific rehearsals
- `/pj-rehearse more` - to run up to 10 rehearsals
- `/pj-rehearse max` - to run up to 25 rehearsals
- `/pj-rehearse auto-ack` - to run up to 5 rehearsals, and add the rehearsals-ack label on success
- `/pj-rehearse abort` - to abort all active rehearsals
- `/pj-rehearse network-access-allowed` - to allow rehearsals of tests that have the `restrict_network_access`
   field set to false. This must be executed by an openshift org member who is not the PR author

Once you are satisfied with the results of the rehearsals, comment `/pj-rehearse ack` to unblock merge.
When the `rehearsals-ack` label is present on your pull request, merge will no longer be blocked by rehearsals.
If you would like the `rehearsals-ack` label removed, comment `/pj-rehearse reject` to re-block merging.

Building
--------

As simple as any other go build:

```bash
go build -gcflags='-N -l' ./cmd/pj-rehearse/
```

Testing
-------

These are the options currently on production, excluding `ghproxy`:

``` bash
./pj-rehearse \
  --dry-run=false \
  --log-level=debug \
  --prowjob-kubeconfig=/var/kubeconfigs/sa.pj-rehearse.app.ci.config \
  --kubeconfig-dir=/var/kubeconfigs \
  --kubeconfig-suffix=config \
  --normal-limit=5 \
  --more-limit=10 \
  --max-limit=25 \
  --sticky-label-author=openshift-bot \
  --endpoint=/ \
  --hmac-secret-file=/etc/webhook/hmac.yaml \
  --github-token-path=/etc/github/oauth \ 
  --config-path=/etc/config/config.yaml
```

After `pj-rehearse` run, we should send our requests to `localhost:8888`, the body of the request should be
authenticated with [HMAC](https://en.wikipedia.org/wiki/HMAC).

The following example send a request with an empty body, the content `{}` should hashed:

```bash
echo -n '{}' | openssl dgst -sha1 -hmac '<secret_from_hmac_yaml>'
# SHA1(stdin)= <resulting_hash>

curl \
-H 'Content-Type: application/json' \
-H 'X-GitHub-Event: pull_request' \
-H 'X-GitHub-Delivery: 0' \
-H 'X-Hub-Signature: sha1=<resulting_hash>' \
-d'{}' \
http://localhost:8888
```

The following JSON have the minimum necessary fields:

```json
{
  "action": "opened",
  "number": 61993,
  "pull_request": { 
    "number": 61993,
    "user": {
      "login": "jianlinliu"
    },  
    "base": {
      "ref": "master",
      "repo": {
        "name": "release",
        "owner": {
          "login": "openshift"
        }       
      }     
    }   
  },
  "repository": { 
    "name": "release",
    "owner": {
      "login": "openshift"
    }   
  } 
}
```
