# Check GH Automation
A tool to check that our bots (`openshift-merge-robot`, `openshift-ci-robot`, and `openshift-cherrypick-robot`) have access to repositories that have CI configured. It also checks that the app used to run it is installed in the repositories. This tool also verifies if `openshift-cherrypick-robot` is an organization member for repos that have the `cherrypick` external prow plugin configured. 

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
--plugin-config=/release/core-services/prow/02_config/ViaQ/_pluginconfig.yaml \
--github-app-id={APP_ID} \
--github-app-private-key-path={CERT_PATH}
```

## Determine Modified Repos from Candidate and JobSpec
If a `candidate-path` to the modified `openshift/release` repo is provided, then the tool will determine which repos have modified/added configurations and _only_ check those.
It is able to determine this by utilizing the `$JOB_SPEC` environment variable that is available in the test pods.
```bash
check-gh-automation \
--bot=openshift-merge-robot \
--bot=openshift-ci-robot \
--candidate-path=/release \
--plugin-config=/path/to/plugin/config.yaml \
--github-app-id={APP_ID} \
--github-app-private-key-path={CERT_PATH}
```

## Pass specific Repo(s) to check
Use the `--repo` parameter for specific repos. Do not supply prow config options or `candidate-path` when using this mode.

## Local Development
Test out the tool locally using the provided script:
```bash
hack/local-check-gh-automation.sh some-org/repo
```
This script will pull necessary secrets from the `app.ci` cluster and run the tool locally, checking the provided repo.
