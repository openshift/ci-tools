# config-migrator

This is a temporary tool helping us migrating repos to build cluster(s).

No image needs to be built out of this tool and we run it locally.

## Steps for migrating repos

* modify the `Migrated(org, repo string)` function to allow the repos that should be migrated.

```bash
$ go install ./cmd/config-migrater/
$ go install ./cmd/ci-operator-prowgen/
$ cd /path_to_release_repo
$ config-migrater --config-dir ./ci-operator/config/ --current-release unused --future-release unused --confirm
### git add . then commit
$ ci-operator-prowgen --from-dir ./ci-operator/config/ --to-dir ./ci-operator/jobs/
### git add . then commit and push

```

Create a PR (PR1) based on the pushed branch, and watch the rehearsal.
If no job is with

If some rehearsal is broken, fix it and iterate the above steps.

If all passed,

* push the latest code for this repo, and create a PR (PR2)
* merge PR2 (with breaking changes) and PR1 (fixing the breaking changes)

