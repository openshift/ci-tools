# private-org-sync

## What
Syncs git repository content from public organizations (openshift, openshift-eng, operator-framework, etc.) to their private mirrors in openshift-priv. Handles shallow clones with exponential depth retry, merge fallback for diverged histories, and org name flattening.

## How it works — full flow

### 1. Discover repositories
- Walks ci-operator config directory to find repos that build official images (`api.BuildsAnyOfficialImages()`)
- Merges with whitelist for repos not in config
- Groups by org/repo, then iterates branches

### 2. Repository naming
- **Flattened orgs** (openshift, openshift-eng, operator-framework, redhat-cne, openshift-assisted, ViaQ by default + `--flatten-org` values): repo name unchanged in openshift-priv
  - Example: `openshift/installer` -> `openshift-priv/installer`
- **Non-flattened orgs**: prefixed with org name
  - Example: `migtools/filebrowser` -> `openshift-priv/migtools-filebrowser`

### 3. Branch filtering
- Checks source branch existence via `git ls-remote --heads` (retries 5x with exponential backoff)
- **Release branches** (`openshift-*`, `release-*`): error if source doesn't exist
- **Misc branches**: skip with warning if source doesn't exist
- If source and destination already at same commit: skip (no-op)

### 4. Exponential depth retry
Starts with a shallow fetch and progressively deepens until push succeeds:

| Depth level | Git flag | Commits fetched |
|---|---|---|
| 1 | `--depth=2` | 2 |
| 2 | `--depth=4` | 4 |
| 3 | `--depth=8` | 8 |
| 4 | `--depth=16` | 16 |
| 5 | `--depth=32` | 32 |
| 6 | `--depth=64` | 64 |
| 7 | `--unshallow` | Full history |

At each level:
1. Fetch from source at current depth
2. Push to destination (`git push --tags {destURL} FETCH_HEAD:refs/heads/{branch}`)
3. If push fails with "shallow update not allowed" or "rejected because remote contains work": increase depth and retry

Each shallow fetch has 3 internal retries for transient "shallow file has changed" errors.

### 5. Merge fallback
When push still fails after unshallowing (histories have diverged):
1. `git fetch {destURL} {branch}` — get destination state
2. `git checkout FETCH_HEAD` — switch to destination
3. `git merge {srcRemote}/{branch} -m "DPTP reconciliation from upstream"` — merge source
4. If merge conflict: `git merge --abort`, log warning, return nil (graceful skip)
5. `git push --tags {destURL} HEAD:{branch}` — push merged result

### 6. Error handling
- Fatal: token file unreadable, git dir creation fails, invalid options
- Skipped: misc branch missing, destination repo missing (unless `--fail-on-missing-destination`), merge conflicts
- All errors accumulated and reported at end

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--token-path` | — | GitHub token file path |
| `--target-org` | — | Destination organization (e.g., openshift-priv) |
| `--config-dir` | — | CI operator config directory |
| `--git-name` | — | Git author name for merge commits |
| `--git-email` | — | Git author email |
| `--flatten-org` | — | Orgs that keep original repo names (repeatable) |
| `--only-org` | — | Mirror only repos from this org |
| `--only-repo` | — | Mirror only this specific repo (org/repo format) |
| `--confirm` | false | Execute real operations |
| `--fail-on-missing-destination` | false | Error if destination repo doesn't exist |

## Key files
- `cmd/private-org-sync/main.go` — full sync logic, mirror(), exponential retry, merge fallback
- `pkg/privateorg/flatten.go` — `MirroredRepoName()`, default flattened orgs

## Deployment
Periodic Prow job ([recent runs](https://prow.ci.openshift.org/?job=periodic-openshift-release-private-org-sync)).
Both source and destination are queried with `git ls-remote`: if they are
already in sync, no further git operations are done. When the destination is
detected to be an empty repo, full `git fetch` is done against a source,
and FETCH_HEAD is then pushed to the destination. When the destination
is a non-empty repository, `git fetch --depth X` is done against the
source and the resulting shallow branch tip is attempted to be pushed.
When this fails, X is exponentially increased and the fetch is retried.
When a threshold is hit, the shallow branch tip is fully fetched with
`--unshallow` and the push is retried again.

By default the tool does not fail when the destination repository does not exist,
which allows running against the full openshift/release while repository mirrors
are created in the private org. Use `--fail-on-missing-destination` to treat
missing destinations as errors.

Errors resulting from merge conflicts are also ignored (see [DPTP-1426][]) so
they do not cause the job to fail and an alert to be fired when there is a
divergence between the repositories during the process of handling a CVE.  Note
that this causes repositories to silently diverge in other cases, such as when
there is a force-push to the source repository.

## Repository Naming Convention

To prevent collisions when multiple organizations have repositories with the same name, this tool uses a special naming convention:
- Repositories from the organization specified by `--only-org` keep their original names
- Repositories from organizations specified by `--flatten-org` keep their original names (can be specified multiple times)
- Repositories from the following default organizations always keep their original names for backwards compatibility:
  - `openshift`
  - `openshift-eng`
  - `operator-framework`
  - `redhat-cne`
  - `openshift-assisted`
  - `ViaQ`
- All other repositories are synced with names prefixed by their source organization: `<source-org>-<repo>`

For example, with `--only-org=openshift --flatten-org=migtools`:
- `openshift/must-gather` → `openshift-priv/must-gather` (from --only-org and default)
- `openshift-eng/ocp-build-data` → `openshift-priv/ocp-build-data` (from default)
- `migtools/crane` → `openshift-priv/crane` (from --flatten-org)
- `redhat-cne/cloud-event-proxy` → `openshift-priv/cloud-event-proxy` (from default)
- `custom-org/some-repo` → `openshift-priv/custom-org-some-repo` (not in flatten list)

This ensures that repositories from different organizations with the same name don't collide in the destination organization.

## Example

```console
$ private-org-sync --config-dir ci-operator/config/ --target-org $ORG --token-path $TOKEN_PATH --only-repo openshift/api --confirm=true --git-dir $( mktemp -d )
INFO[0000] Syncing content between locations             branch=master (more fields...)
INFO[0001] Destination is an empty repo: will do a full fetch right away  branch=master (more fields...)
INFO[0001] Fetching from source (full fetch)             branch=master (more fields...)
INFO[0005] Pushing to destination                        branch=master (more fields...)
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

$ private-org-sync --config-dir ci-operator/config/ --target-org $ORG --token-path $TOKEN_PATH --only-repo openshift/api --confirm=true --git-dir $( mktemp -d )
INFO[0000] Syncing content between locations             branch=master (more fields...)
INFO[0001] Branches are already in sync                  branch=master (more fields...)
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
INFO[0000] Syncing content between locations             branch=master destination=dst/repo@master local-repo=tmp/src/repo org=src repo=repo source=src/repo@master source-file=src-repo-master.yaml variant=
INFO[0000] Fetching from source (--depth=2)              branch=master destination=dst/repo@master local-repo=tmp/src/repo org=src repo=repo source=src/repo@master source-file=src-repo-master.yaml variant=
INFO[0000] Pushing to destination (dry-run)              branch=master destination=dst/repo@master local-repo=tmp/src/repo org=src repo=repo source=src/repo@master source-file=src-repo-master.yaml variant=
INFO[0000] Trying to fetch source and destination full history and perform a merge  branch=master destination=dst/repo@master local-repo=tmp/src/repo org=src repo=repo source=src/repo@master source-file=src-repo-master.yaml variant=
WARN[0000] error occurred while fetching remote and merge  branch=master destination=dst/repo@master error="[failed to merge src-repo/master: failed with 128 exit-code: fatal: refusing to merge unrelated histories\n, failed to perform merge --abort: failed with 128 exit-code: fatal: There is no merge to abort (MERGE_HEAD missing).\n]" local-repo=tmp/src/repo org=src repo=repo source=src/repo@master source-file=src-repo-master.yaml variant=
```

[DPTP-1426]: https://issues.redhat.com/browse/DPTP-1426
