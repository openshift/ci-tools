# Check GH Automation
A tool to check that our bots (`openshift-merge-robot` and `openshift-ci-robot`) have access to repositories that have CI configured.
It also checks that the app used to run it is installed in the repositories.
This can be run in multiple modes:

## Pass Prow Config Options
The standard Prow config options can be supplied, and the tool will check _every_ repo with configurations:
```bash
check-gh-automation \
--bot=openshift-merge-robot \
--bot=openshift-ci-robot \
--config-path=/release/core-services/prow/02_config/_config.yaml \
--supplemental-prow-config-dir=/release/core-services/prow/02_config \
--job-config-path=/release/ci-operator/jobs/ \
--github-app-id={APP_ID} \
--github-app-private-key-path={CERT_PATH}
```

## Determine Modified Repos from Candidate and JobSpec
If a `candidate-path` to the modified `openshift/release` repo is provided, then the tool will determine which repos have modified/added configurations and _only_ check those.
It is able to determine this by utilizing the `$JOB_SPEC` environment variable that is available in the test pods.
```bash
check-gh-automation
--bot=openshift-merge-robot \
--bot=openshift-ci-robot \
--candidate-path=/release \
--github-app-id={APP_ID} \
--github-app-private-key-path={CERT_PATH}
```

## Pass specific Repo(s) to check
The `--repo` parameter can be used to pass one or more repos in to be checked.
When using this mode do not supply the prow config options or the `candidate-path`

## Local Development
A `hack/local-check-gh-automation.sh` script exists to test out the tool locally. Usage is simple:
```bash
hack/local-check-gh-automation.sh some-org/repo
```
The script will pull the necessary secrets from the `app.ci` cluster and run the tool locally, checking the provided repo.
