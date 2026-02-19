# backport-verifier

Prow plugin that validates backports by verifying commits come from merged PRs in upstream repositories.

## Overview

The backport-verifier is a Prow webhook plugin that automatically validates backport pull requests. It checks that commits in a backport PR reference valid, merged upstream pull requests.

## Features

- Validates commit messages follow the `UPSTREAM: <PR_NUMBER>: <message>` format
- Verifies referenced upstream PRs exist and are merged
- Adds labels to PRs based on validation status:
  - `backports/validated-commits` - All commits validated
  - `backports/unvalidated-commits` - Some commits could not be validated
- Responds to `/validate-backports` command in PR comments

## Configuration

The tool requires a configuration file mapping downstream repositories to their upstream counterparts:

```yaml
repositories:
  openshift/kubernetes: kubernetes/kubernetes
  openshift/etcd: etcd-io/etcd
```

## Usage

```bash
backport-verifier \
  --config-path=/path/to/config.yaml \
  --hmac-secret-file=/path/to/hmac-secret \
  --github-token-path=/path/to/token
```

## How It Works

1. Listens for pull request events and issue comments
2. Extracts commit messages matching `UPSTREAM: <PR_NUMBER>: <message>` pattern
3. Verifies each referenced upstream PR exists and is merged
4. Updates PR labels and comments based on validation results

## Commands

- `/validate-backports` - Manually trigger validation for a PR

