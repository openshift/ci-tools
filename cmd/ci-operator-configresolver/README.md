# ci-operator-configresolver

HTTP server that resolves and serves ci-operator configurations with resolved references.

## Overview

The configresolver provides a web service that loads ci-operator configurations, resolves all references (base images, includes, etc.), and serves them via HTTP. It also provides a web UI for browsing configurations.

## Features

- Resolves ci-operator config references in real-time
- Serves resolved configs via REST API
- Web UI for browsing configurations
- Watches for config changes and reloads automatically
- Integrates with step registry

## Usage

```bash
ci-operator-configresolver \
  --config-path=/path/to/configs \
  --registry-path=/path/to/registry \
  --address=:8080 \
  --ui-address=:8081
```

## Options

- `--config-path`: Path to ci-operator config directory
- `--registry-path`: Path to step registry
- `--address`: Address for API server (default: :8080)
- `--ui-address`: Address for web UI (default: :8081)
- `--release-repo-git-sync-path`: Path for git-synced release repo
- `--log-level`: Logging level

## API Endpoints

- `GET /config` - Get resolved config for org/repo/branch
- `GET /resolve` - Resolve config with parameters
- Web UI available at the UI address

## Related

- [pkg/configresolver](../../pkg/api/configresolver) - Config resolution logic
- [pkg/registry](../../pkg/registry) - Step registry

