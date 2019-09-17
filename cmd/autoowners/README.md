# Populating `OWNERS` and `OWNERS_ALIASES`

This utility updates the OWNERS files from remote Openshift repositories.

```
$ ./autoowners -h
Usage of ./autoowners:
  -assign string
        The github username or group name to assign the created pull request to. (default "openshift/openshift-team-developer-productivity-test-platform")
  -git-email string
        The email to use on the git commit. Requires --git-name. If not specified, uses the system default.
  -git-name string
        The name to use on the git commit. Requires --git-email. If not specified, uses the system default.
  -github-login string
        The GitHub username to use. (default "openshift-bot")
  -github-token string
        The path to the GitHub token file.
  -ignore-repo value
        The repo for which syncing OWNERS file is disabled.
  -target-dir string
        The directory containing the target repo.

```

Upstream repositories are calculated from `ci-operator/jobs/{organization}/{repository}`.
For example, the presence of [`ci-operator/jobs/openshift/origin`](../../ci-operator/jobs/openshift/origin) inserts [openshift/origin][] as an upstream repository.

The `HEAD` branch for each upstream repository is pulled to extract its `OWNERS` and `OWNERS_ALIASES`.
If `OWNERS` is missing, the utility will ignore `OWNERS_ALIASES`, even if it is present upstream.

Any aliases present in the upstream `OWNERS` file will be resolved to the set of usernames they represent in the associated
`OWNERS_ALIASES` file.  The local `OWNERS` files will therefore not contain any alias names.  This avoids any conflicts between 
upstream alias names coming from  different repos.

The utility also iterates through the `ci-operator/{type}/{organization}/{repository}` for `{type}` in `config`, `jobs`, and `templates`, writing `OWNERS` to reflect the upstream configuration.
If the upstream does not have an `OWNERS` file, the utility will ignore syncing it for those paths.
