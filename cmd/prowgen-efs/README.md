# prowgen-efs

`prowgen-efs` publishes generated Prow jobs to EFS for branches whose jobs are
managed outside Git. CI configurations remain in `openshift/release`; their
generated job YAML no longer needs to be committed. Normal Prowgen runs skip
those branches using the same managed-repository configuration.

The command selects every managed configuration, including variants, and runs
the existing `ci-operator-prowgen` and `sanitize-prow-jobs` tools. It preserves
existing job overrides and cluster assignments, then validates the candidate
alongside Git jobs and other writers' visible EFS jobs using Prow `checkconfig`.
These three tools and Git must be on `PATH`; the publisher image provides them.
The image is built and promoted through ci-operator.

Run a preview against a release checkout:

```sh
prowgen-efs --release-repo ../release \
  --efs-root /tmp/prowgen-efs \
  --candidate-dir /tmp/prowgen-efs-preview
```

The preview directory must be new and outside the EFS root. Use
`--managed-repos-config` to select another ownership file. Relative paths for
that flag resolve inside the release checkout. Preview mode generates and
sanitizes jobs without changing visible EFS output or checking for Git/EFS
collisions, so it can be used before removing the Git copies.

With `--publish`, the command holds an EFS lock, fetches the latest
`release/main`, and uses an isolated checkout for older triggers. The ownership
file must come from that checkout. Validation must pass before publication;
Git/EFS filename and job-name collisions are rejected.

The publisher owns only files recorded in `..prowgen/inventory`, under
`generated/`. It replaces files with atomic renames and removes stale owned
files. A recovery inventory lets the next run reconcile interrupted writes;
publication across multiple files is not transactional. Temporary work and
state stay under `..prowgen/`, which Prow ignores. Hand-written jobs must be
separated from generated shards before enrollment. Reader rollout and the
reviewed removal of Git jobs are configured in `openshift/release`.
