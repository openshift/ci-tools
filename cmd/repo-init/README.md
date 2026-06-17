# repo-init

## What
Interactive onboarding tool for bootstrapping new repositories into OpenShift CI. Generates Prow configuration (Tide queries, plugin config) and ci-operator configuration (build root, tests, images, promotions) from user-provided inputs. Operates in three modes:

- **CLI**: Interactive terminal prompts for local use
- **API**: REST server for programmatic config generation, validation, and PR creation
- **UI**: Embedded React/PatternFly web wizard (serves static frontend assets)

## How it works -- full flow

### CLI mode (`--mode cli`)

1. **Collect information** interactively (or via `--config` JSON flag):
   - Org, repo, branch
   - Whether it promotes images, promotes with OpenShift, needs base/OS images
   - Go version, build commands, test build commands, canonical Go import path
   - Unit/integration test scripts (name, from-image, command)
   - End-to-end tests (name, cluster profile, command, workflow, CLI requirement)
   - Operator bundle configuration (optional)
   - Release type and version for non-promoting repos with e2e tests

2. **Check for existing config**: If a config already exists at `ci-operator/config/<org>/<repo>` in the release repo, abort.

3. **Update Prow config** (`updateProwConfig`):
   - Load existing Prow config from the release repo
   - Check if Tide queries already exist for this org/repo; if so, skip
   - Copy Tide queries from a reference repo: `openshift/cluster-version-operator` for OCP components, `openshift/ci-tools` for non-OCP repos
   - Replace the org/repo in the copied queries
   - Write the per-repo prowconfig YAML to the appropriate sharded path

4. **Update plugin config** (`updatePluginConfig`):
   - Load the plugin config from `core-services/prow/02_config`
   - If neither org nor repo has plugins configured, add all plugins from `openshift` + `openshift/origin`
   - If org has plugins but repo doesn't, add only the repo-specific plugins missing from org-level
   - Add external plugins if not configured at org level
   - Add `approve` (self-approval disabled) and `lgtm` (review acts as lgtm) config for the repo

5. **Generate ci-operator config** (`generateCIOperatorConfig`):
   - Build root: Go image from `openshift/release:golang-<version>`
   - Base images: `base` (from promotion target) and/or `os` (centos:7) if needed
   - Promotion: configure targets matching openshift/origin's namespace/name
   - Releases: `initial` and `latest` integration releases for promoting repos
   - Tests: container tests from user input, e2e tests with multi-stage configurations
   - Operator bundle: OLM operator testing configuration
   - Resources: default limits (4Gi memory) and requests (200Mi memory, 100m CPU)
   - Write to `ci-operator/config/<org>/<repo>/<org>-<repo>-<branch>.yaml`

6. **Print replay command**: Output the JSON config so the run can be reproduced non-interactively.

### API mode (`--mode api`)

Runs an HTTP server with the following endpoints:

| Endpoint | Method | What it does |
|---|---|---|
| `POST /api/auth` | POST | OAuth flow: exchange GitHub code for access token, return user info |
| `GET /api/cluster-profiles` | GET | List available cluster profiles |
| `GET /api/configs?org=X&repo=Y` | GET | Load existing ci-operator configs for an org/repo |
| `POST /api/configs` | POST | Generate config from `initConfig` JSON; optionally create PR (`?generatePR=true`) or just convert (`?conversionOnly=true`) |
| `POST /api/config-validations` | POST | Validate partial or full configs (base images, container images, tests, operator bundles, operator substitutions) |
| `GET /api/server-configs` | GET | Return non-secret server config (GitHub client ID, redirect URI) |

The API server maintains a pool of `--num-repos` (default 4) local clones of openshift/release, with locking to prevent concurrent access conflicts. When generating configs with PR creation:
- Runs `ci-operator-checkconfig`, `ci-operator-prowgen`, and `sanitize-prow-jobs` to mimic `make jobs`
- Pushes changes to the user's fork and returns a PR creation URL

### UI mode (`--mode ui`)

Serves an embedded React application (built from `cmd/repo-init/frontend/dist`) as static assets. The UI communicates with the API server for all backend operations.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--mode` | `cli` | Operating mode: `cli`, `api`, or `ui` |
| `--release-repo` | `""` | Path to the root of the openshift/release repository (required for CLI mode) |
| `--config` | `""` | JSON configuration to use instead of interactive prompts (CLI mode) |
| `--port` | `0` | HTTP server port (required for API and UI modes) |
| `--num-repos` | `4` | Number of openshift/release clones to maintain for API mode |
| `--server-config-path` | `""` | Directory containing server config files: `github-client-id`, `github-client-secret`, `github-redirect-uri` |
| `--disable-cors` | `false` | Disable CORS restrictions (for local development) |
| `--loglevel` | `debug` | Log level |
| `--log-style` | `json` | Log format: `json` or `text` |
| GitHub flags | -- | `--github-token-path`, `--github-endpoint`, etc. via `GitHubOptions` |
| Instrumentation flags | -- | `--health-port`, `--metrics-port` via `InstrumentationOptions` |

## Key files

- `cmd/repo-init/main.go` -- entry point, CLI mode implementation: interactive prompts, config generation (`generateCIOperatorConfig`), Prow/plugin config updates
- `cmd/repo-init/api.go` -- API server: REST handlers, config validation, PR creation, release repo pool management
- `cmd/repo-init/frontend.go` -- UI server: embedded static asset serving
- `cmd/repo-init/frontend/` -- React/PatternFly web application source

## Deployment
- **API server**: Deployment on app.ci (`repo-init-apiserver`), `ci` namespace, port 8080. Maintains multiple clones of openshift/release for concurrent requests.
- **UI server**: Deployment on app.ci (`repo-init-ui`), `ci` namespace, port 8080. Serves the React frontend.
- **CLI**: Local developer usage only.
The `repo-init` component allows a user to on-board a new repository to the CI Test Platform.
## CLI

To run the tool in CLI mode, execute

```shell
repo-init --mode=cli --release-repo=/path/to/release/repo
```

### API

The API is used by the UI component to authenticate against GitHub, validate configurations, generate configurations, and also to generate pull requests against the `release` repository for new configurations.
To start up the API you may run something like

```shell
repo-init --mode=api --port=8080 --github-token-path=/tmp/token --github-endpoint=https://api.github.com --num-repos=4 --server-config-path=/tmp/serverconfig
```

#### Github OAuth

In order to run the application locally you must configure a [Github OAuth app](https://docs.github.com/en/developers/apps/building-oauth-apps/creating-an-oauth-app) to authenticate.
The following settings should be configured:
* 'Homepage URL' should be set to `http://localhost:9000`
* 'Authorization callback URL' should be set to `http://localhost:9000/login`

Now the `/tmp/serverconfig` directory (or where ever you set `--server-config-path` to) can be modified to contain the following values from your OAuth app:
* `github-client-id` - this file should contain the client ID of the OAuth application in GitHub.
* `github-client-secret` - this file should contain the client secret of the OAuth application in GitHub.
* `github-redirect-uri`  - this file should contain the redirect URI of the OAuth application in GitHub. (`http://localhost:9000/login`)

### UI

The UI is a React/PatternFly based web-app that presents the on-boarding flow as a Wizard component. Here, the user may enter details about the component, such as build information (is it an optional operator build, what tests need to be executed, etc.). At the end of the workflow,
a ci-operator config will be generated. The user may at this point choose to simply push that config to their own release repo, or also to create a pull request for the upstream release repo.

To run the UI locally you may run something like:

```shell
npm run start:dev
```

from within the `frontend` dir. This will start the UI in development mode where any changes you make will be hot-reloaded.

Additionally, you may execute the built UI like so:

```shell
repo-init --mode=ui --port=9000 --metrics-port=9001 --health-port=9002
```

Note that you must first have built the UI by executing:

```shell
npm run build
```

from within the `frontend` dir.

## Development

For local development you should have a `/cmd/repo-init/frontend/.env` file with something like this in it:

```
REACT_APP_API_URI=http://localhost:8080/api
```

The easiest way to run the API/UI locally is to execute the `hack/local-repo-init-ui.sh` script, like this:

```shell
hack/local-repo-init-ui.sh start
```

or

```shell
hack/local-repo-init-ui.sh stop
```

to stop a running instance and clean up temporary files.

The root `Makefile` contains some convenience targets for deploying a test instance of the `repo-init` API and UI based on a current pull request.

```shell
make pr-deploy-repo-init-api
```

and

```shell
make pr-deploy-repo-init-ui
```

After this, you should have a working copy of the `repo-init` component deployed that you can test with.
