The job-run-aggregator finds multiple runs of the same job for the same payload and analyzes the overall result
and the individual junit results.

The analysis allows failures within (ideally) a standard deviation of the norm for individual tests.
This will allow a single payload to checked by multiple parallel job runs and the average results for each test
checked to ensure that a regression hasn't happened.
That property allows us to have less than perfect test results to start and still be able to latch improvements into
the failure percentages, giving a path to improvement.

## Development and Debugging tips

When you run this in debugging mode, use a credential that has read-only access. This way, you
can set breakpoints and study the behavior without risk of overwriting anything.
You will, of course, get "permission denied" errors if write access is required.
At that point, you can (cautiously) switch to a credential that has write access.

This is a way to build (without optimazations) for debugging:

```
cd ci-tools
go build -gcflags='-N -l' `grep "module " go.mod |awk '{print $2}'`/cmd/job-run-aggregator
```

Example command lines:

```
# Run in dry run mode with read credential
dlv exec ./job-run-aggregator -- upload-test-runs --dry-run --bigquery-dataset ci_data --google-service-account-credential-file ~/project-reader.json

# Run command to create tables in write mode (in the a dataset called "my_dataset" (someone's dataset created for testing)
dlv exec ./job-run-aggregator -- create-releases --bigquery-dataset my_dataset --google-service-account-credential-file ~/project-write.json

# This will run and create tables in the "my_dataset" dataset:
dlv exec ./job-run-aggregator -- create-releases --bigquery-dataset my_dataset --google-service-account-credential-file ~/project-write.json

# This will create the Jobs table:
dlv exec ./job-run-aggregator -- create-tables --bigquery-dataset my_dataset --google-service-account-credential-file ~/project-write.json

# This will run and insert the jobs in "Jobs" table
dlv exec ./job-run-aggregator -- prime-job-table --bigquery-dataset my_dataset --google-service-account-credential-file ~/project-write.json
```

Here's how to reproduce and (hopefully) fix things if the linter (run as part of CI) fails:

```
# Get the linter from https://golangci-lint.run/usage/install

# After you make your code changes, run the linter
cd ci-tools
./hack/lint.sh

# If something fails, this might fix it; then rerun the linter
gofmt -s -w <someFileThatFailsLint>/cmd.go
```

## Running Locally

### Analyze Historical Data

> Note: We also contain a helper script at the root of this repo that can be used to generate the files run `./hack/run-job-aggregator-analyzer.sh` for a list of options.

You can compare the historical data locally, the documentation on how to download the recent historical data can be found [here](https://docs.ci.openshift.org/release-oversight/disruption-testing/data-architecture/#query).

```sh
# Disruptions data
./job-run-aggregator analyze-historical-data  \
--current ./<current-disruptions>.json \
--data-type disruptions \
--new ./<new-disruptions>.json \
--leeway 30

# Alerts data
./job-run-aggregator analyze-historical-data  \
--current ./<current-alerts>.json \
--data-type alerts \
--new ./<new-alerts>.json \
--leeway 30
```

You can also automatically pull the latest from BigQuery if you have read credentials.

```sh
# Disruptions data
./job-run-aggregator analyze-historical-data  \
--current ./<current-disruptions>.json \
--data-type disruptions \
--leeway 30 \
--google-service-account-credential-file <gcs_creds.json>

# Alerts data
./job-run-aggregator analyze-historical-data  \
--current ./<current-alerts>.json \
--data-type alerts \
--leeway 30 \
--google-service-account-credential-file <gcs_creds.json>
```
