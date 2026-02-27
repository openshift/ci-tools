# retester

Automatically retests failed jobs.

## Overview

Retester monitors job failures and automatically triggers retests for failed jobs based on configured policies.

## Usage

```bash
retester \
  --prow-config=/path/to/config.yaml \
  --github-token-path=/path/to/token
```

## Options

- `--prow-config`: Path to Prow configuration
- `--github-token-path`: Path to GitHub token
- `--dry-run`: Show what would be done without making changes

## Related

- [pkg/retester](../../pkg/retester) - Retest logic and policies

