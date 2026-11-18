The `multi-pr-prow-plugin` is an external prow plugin that facilitates running presubmit tests
from sources built using multiple pull requests. The included pull requests can be from the same, or a different, repo.
It creates and manages GitHub `check_runs` to keep share the state and logs of the jobs with the user.

## Usage

Comment on a pull request (the origin PR) with:

```
/testwith <org>/<repo>/<branch>/[<variant>/]<test> [<org>/<repo>#<number> ...]
```

- The origin PR is always included in the job.
- The additional PRs are optional. They can also be given as GitHub pull request URLs.
- PRs against the org/repo containing the test are merged onto the requested branch, and form the primary source of the job.
- When none of the included PRs is against the org/repo containing the test, the test runs against the current head of the
  requested branch, and every included PR, including the origin PR, is cloned as an additional source.
  The job name then also contains the org, repo, branch and variant of the test.
- `/testwith abort` aborts the active jobs triggered from the origin PR.

Examples:

```
/testwith openshift/kubernetes/master/e2e openshift/kubernetes#1234 openshift/installer#999
/testwith openshift/cluster-version-operator/main/e2e-agnostic-ovn
```
