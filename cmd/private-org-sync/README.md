# Private Org Sync

This tool automatically syncs code from which "official" images are built
from their public git repo locations to their mirrors in private GitHub
organization. It is intended to be run in a Prow periodic job with an interval
of less than an hour.

Both source and destination are queried with `git ls-remote`: if they are
already in sync, no further git operations are done. When the destination is
detected to be an empty repo, full `git fetch` is done against a source,
and FETCH_HEAD is then pushed to the destination. When the destination
is a non-empty repository, `git fetch --depth X` is done against the
source and the resulting shallow branch tip is attempted to be pushed.
When this fails, X is exponentially increased and the fetch is retried.
When a threshold is hit, the shallow branch tip is fully fetched with
`--unshallow` and the push is retried again.

Currently the tool does not fail when the destination repository does not exist
at all: this should allow running against full openshift/release while
repository mirrors are created in the private org.

Errors resulting from merge conflicts are also ignored (see [DPTP-1426][]) so
they do not cause the job to fail and an alert to be fired when there is a
divergence between the repositories during the process of handling a CVE.  Note
that this causes repositories to silently diverge in other cases, such as when
there is a force-push to the source repository.

## Example

```console
$ private-org-sync --config-path ci-operator/config/ --target-org $ORG --token-path $TOKEN_PATH --only-repo openshift/api --confirm=true --git-dir $( mktemp -d )
INFO[0000] Syncing content between locations             branch=main (more fields...)
INFO[0001] Destination is an empty repo: will do a full fetch right away  branch=main (more fields...)
INFO[0001] Fetching from source (full fetch)             branch=main (more fields...)
INFO[0005] Pushing to destination                        branch=main (more fields...)
INFO[0033] Syncing content between locations             branch=release-4.1 (more fields...)
INFO[0034] Fetching from source (--depth=2)              branch=release-4.1 (more fields...)
INFO[0035] Pushing to destination                        branch=release-4.1 (more fields...)
INFO[0038] failed to push to destination, retrying with deeper fetch  branch=release-4.1 (more fields...)
INFO[0038] Fetching from source (--depth=4)              branch=release-4.1 (more fields...)
INFO[0039] Pushing to destination                        branch=release-4.1 (more fields...)
INFO[0043] Syncing content between locations             branch=release-4.2 (more fields...)
INFO[0044] Fetching from source (--depth=2)              branch=release-4.2 (more fields...)
INFO[0045] Pushing to destination                        branch=release-4.2 (more fields...)
INFO[0047] failed to push to destination, retrying with deeper fetch  branch=release-4.2 (more fields...)
INFO[0047] Fetching from source (--depth=4)              branch=release-4.2 (more fields...)
INFO[0049] Pushing to destination                        branch=release-4.2 (more fields...)
INFO[0052] Syncing content between locations             branch=release-4.3 (more fields...)
INFO[0053] Fetching from source (--depth=2)              branch=release-4.3 (more fields...)
INFO[0057] Pushing to destination                        branch=release-4.3 (more fields...)
INFO[0066] failed to push to destination, retrying with deeper fetch  branch=release-4.3 (more fields...)
INFO[0066] Fetching from source (--depth=4)              branch=release-4.3 (more fields...)
INFO[0068] Pushing to destination                        branch=release-4.3 (more fields...)
INFO[0076] failed to push to destination, retrying with deeper fetch  branch=release-4.3 (more fields...)
INFO[0076] Fetching from source (--depth=8)              branch=release-4.3 (more fields...)
INFO[0078] Pushing to destination                        branch=release-4.3 (more fields...)
INFO[0081] failed to push to destination, retrying with deeper fetch  branch=release-4.3 (more fields...)
INFO[0081] Fetching from source (--depth=16)             branch=release-4.3 (more fields...)
INFO[0082] Pushing to destination                        branch=release-4.3 (more fields...)
INFO[0087] Syncing content between locations             branch=release-4.4 (more fields...)
INFO[0088] Fetching from source (--depth=2)              branch=release-4.4 (more fields...)
INFO[0089] Pushing to destination                        branch=release-4.4 (more fields...)
INFO[0093] Syncing content between locations             branch=release-4.5 (more fields...)
INFO[0094] Fetching from source (--depth=2)              branch=release-4.5 (more fields...)
INFO[0095] Pushing to destination                        branch=release-4.5 (more fields...)

$ private-org-sync --config-path ci-operator/config/ --target-org $ORG --token-path $TOKEN_PATH --only-repo openshift/api --confirm=true --git-dir $( mktemp -d )
INFO[0000] Syncing content between locations             branch=main (more fields...)
INFO[0001] Branches are already in sync                  branch=main (more fields...)
INFO[0001] Syncing content between locations             branch=release-4.1 (more fields...)
INFO[0002] Branches are already in sync                  branch=release-4.1 (more fields...)
INFO[0002] Syncing content between locations             branch=release-4.2 (more fields...)
INFO[0003] Branches are already in sync                  branch=release-4.2 (more fields...)
INFO[0003] Syncing content between locations             branch=release-4.3 (more fields...)
INFO[0004] Branches are already in sync                  branch=release-4.3 (more fields...)
INFO[0004] Syncing content between locations             branch=release-4.4 (more fields...)
INFO[0005] Branches are already in sync                  branch=release-4.4 (more fields...)
INFO[0005] Syncing content between locations             branch=release-4.5 (more fields...)
INFO[0006] Branches are already in sync                  branch=release-4.5 (more fields...)
```

## Testing

The `--prefix` argument can be used for local tests.  Its default value points
all operations to GitHub, but another host or a local directory can be used
instead:

```console
$ mkdir --parents config/src/repo src tmp
$ cat > config/src/repo/src-repo-master.yaml <<EOF
promotion: {"namespace":"ocp","name":4.13}
resources: {"*":{"requests":{"cpu":1}}}
tests: [{"as":"test","commands":"commands","container":{"from":"src"}}]
EOF
$ git init --quiet src/repo
$ git init --quiet dst/repo
$ git -C src/repo commit --allow-empty --message initial
$ git -C dst/repo commit --allow-empty --message initial
$ private-org-sync \
    --prefix $PWD --token-path /dev/null --config-dir config --target-org dst \
    --git-name test --git-email test --git-dir tmp
INFO[0000] Syncing content between locations             branch=main destination=dst/repo@main local-repo=tmp/src/repo org=src repo=repo source=src/repo@main source-file=src-repo-master.yaml variant=
INFO[0000] Fetching from source (--depth=2)              branch=main destination=dst/repo@main local-repo=tmp/src/repo org=src repo=repo source=src/repo@main source-file=src-repo-master.yaml variant=
INFO[0000] Pushing to destination (dry-run)              branch=main destination=dst/repo@main local-repo=tmp/src/repo org=src repo=repo source=src/repo@main source-file=src-repo-master.yaml variant=
INFO[0000] Trying to fetch source and destination full history and perform a merge  branch=main destination=dst/repo@main local-repo=tmp/src/repo org=src repo=repo source=src/repo@main source-file=src-repo-master.yaml variant=
WARN[0000] error occurred while fetching remote and merge  branch=main destination=dst/repo@main error="[failed to merge src-repo/main: failed with 128 exit-code: fatal: refusing to merge unrelated histories\n, failed to perform merge --abort: failed with 128 exit-code: fatal: There is no merge to abort (MERGE_HEAD missing).\n]" local-repo=tmp/src/repo org=src repo=repo source=src/repo@main source-file=src-repo-master.yaml variant=
```

[DPTP-1426]: https://issues.redhat.com/browse/DPTP-1426
