The `multi-pr-prow-plugin` is an external prow plugin that facilitates running presubmit tests
from sources built using multiple pull requests. The included pull requests can be from the same, or a different, repo.
It creates and manages GitHub `check_runs` to keep share the state and logs of the jobs with the user.
