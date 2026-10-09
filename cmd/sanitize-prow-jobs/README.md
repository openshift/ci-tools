# Sanitize prow jobs

`sanitize-prow-jobs` is a small tool that:

* Makes sure all jobs are formatted the same way to keep diffs small
* Applies defaults to them

Use `--cluster-only` to apply only cluster assignments. Changed files are
replaced atomically.
