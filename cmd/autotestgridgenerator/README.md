# autotestgridgenerator

Automatically generates and updates OpenShift testgrid definitions by running the testgrid-config-generator and creating pull requests.

## Overview

This tool automates the process of updating testgrid configurations in the kubernetes/test-infra repository. It runs the testgrid-config-generator command and creates/updates pull requests with the generated changes.

## Usage

```bash
autotestgridgenerator \
  --testgrid-config=/path/to/testgrid/config \
  --release-config=/path/to/release/config \
  --prow-jobs-dir=/path/to/prow/jobs \
  --allow-list=/path/to/allow-list \
  --github-token-path=/path/to/token
```

## Options

- `--testgrid-config`: Directory where testgrid output will be stored
- `--release-config`: Directory of release config files
- `--prow-jobs-dir`: Directory where prow-job configs are stored
- `--allow-list`: File containing release-type information to override defaults
- `--github-login`: GitHub username to use (default: openshift-bot)
- `--assign`: GitHub username or team to assign PR to (default: openshift/test-platform)
- `--working-dir`: Working directory for git operations

## How It Works

1. Executes `/usr/bin/testgrid-config-generator` with provided configuration
2. Generates testgrid configuration files
3. Creates or updates a pull request in kubernetes/test-infra with the changes
4. PR title format: "Update OpenShift testgrid definitions by auto-testgrid-generator job at <timestamp>"

## Related Tools

- [testgrid-config-generator](../testgrid-config-generator) - Generates testgrid configurations

