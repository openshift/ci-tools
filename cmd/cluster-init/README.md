# cluster-init

## What
CLI tool that manages the full lifecycle of a Test Platform (TP) build cluster: provisioning infrastructure on a cloud provider, installing OCP, and generating/updating all the onboarding configuration needed to integrate the cluster into CI. Uses a cobra command tree with two top-level subcommands: `provision` and `onboard`.

## How it works -- full flow

### `provision` subcommand tree

#### `provision aws create-stacks`
Creates CloudFormation stacks required for a build cluster on AWS. Loads a `cluster-install.yaml` spec, initializes an AWS provider with default config, and executes the `CreateAWSStacksStep`. Requires a properly configured AWS profile (named profile or environment variables).

#### `provision ocp create <target>`
Runs the OCP installer in stages. The `<target>` argument selects which stage to execute:
1. `install-config` -- generates `install-config.yaml` from the cluster-install spec
2. `manifests` -- runs `openshift-install create manifests`
3. `cluster` -- runs `openshift-install create cluster`

Each stage is a `types.Step` that shells out to the installer binary. Commands must be run in sequence (install-config, then manifests, then cluster).

### `onboard` subcommand tree

#### `onboard config generate`
Generates all configuration files for a newly provisioned cluster:
1. Loads the `cluster-install.yaml` spec and resolves the `--install-base` working directory.
2. Connects to the cluster using the admin kubeconfig from the install directory.
3. Pulls runtime info from the live cluster: `Infrastructure` CR, `install-config` from `kube-system/cluster-config-v1`, CoreOS stream metadata from `openshift-machine-config-operator/coreos-bootimages`.
4. Runs a sequence of onboarding steps (each a `types.Step`):
   - ProwJob configuration
   - Build cluster directory scaffolding
   - OAuth template generation
   - ci-secret-bootstrap config update
   - ci-secret-generator config update
   - Sanitize prowjob config
   - Sync rover group
   - Prow plugin config
   - Dex, certificates, Cloudability agent manifests
   - Common symlinks
   - Multi-arch builder controller, tuning operator
   - Image registry, OpenShift monitoring, passthrough, nested podman manifests
   - Cloud credential manifests (if `CredentialsMode == Manual`)
   - Cloud-specific steps (AWS: CI scheduling webhook, machine sets via CloudFormation)
   - Build cluster step and cert-manager (generate-only, skipped during update)

#### `onboard config update`
Bulk-updates configuration for multiple existing clusters:
1. Loads all `cluster-install.yaml` files from `--cluster-install-dir` (defaults to `<release-repo>/clusters/`).
2. Loads kubeconfigs for all clusters via standard Prow kubernetes flags.
3. For each cluster with a valid kubeconfig, connects, pulls runtime info, and runs the same config steps as `generate` -- but with `update=true`, which:
   - Skips the build-cluster and cert-manager steps.
   - Uses cluster-sourced AWS config (reads from cluster objects) instead of hardcoded defaults.
4. Clusters with missing or invalid kubeconfigs are skipped with a warning (non-fatal).

### Registered API schemes
Route v1, ImageRegistry v1, Image v1, Config v1, Auth v1, CloudCredential v1 -- these are registered at startup so the tool can interact with OpenShift-specific resources.

## Flags

### Global (persistent)
| Flag | Default | What it controls |
|---|---|---|
| `--cluster-install` | `""` | Path to `cluster-install.yaml` |
| `--install-base` | `""` | Working directory for install artifacts |

### `onboard config generate`
| Flag | Default | What it controls |
|---|---|---|
| `--release-repo` | (required) | Path to local openshift/release checkout |
| `--release-branch` | `main` | Branch name in release repo |

### `onboard config update`
| Flag | Default | What it controls |
|---|---|---|
| `--release-repo` | (required) | Path to local openshift/release checkout |
| `--release-branch` | `main` | Branch name in release repo |
| `--cluster-install-dir` | `""` | Directory containing cluster-install files (defaults to `<release-repo>/clusters/`) |
| Prow kubernetes flags | -- | Standard multi-cluster kubeconfig flags |

## Key files
- `cmd/cluster-init/main.go` -- entry point, scheme registration, root command
- `cmd/cluster-init/cmd/onboard/onboard.go` -- `onboard` subcommand
- `cmd/cluster-init/cmd/onboard/config/config.go` -- `config` subcommand, step orchestration, cloud-specific step registration
- `cmd/cluster-init/cmd/onboard/config/generate.go` -- `generate` subcommand
- `cmd/cluster-init/cmd/onboard/config/update.go` -- `update` subcommand (bulk multi-cluster)
- `cmd/cluster-init/cmd/provision/provision.go` -- `provision` subcommand
- `cmd/cluster-init/cmd/provision/aws.go` -- `provision aws create-stacks`
- `cmd/cluster-init/cmd/provision/ocp.go` -- `provision ocp create`
- `cmd/cluster-init/runtime/runtime.go` -- shared runtime utilities (`BuildCmd`, `RunCmd`)
- `cmd/cluster-init/runtime/aws/` -- AWS config providers (from-cluster vs. from-defaults)
- `pkg/clusterinit/clusterinstall/` -- cluster-install spec loading and finalization
- `pkg/clusterinit/onboard/` -- all onboarding step implementations
- `pkg/clusterinit/provision/` -- provisioning step implementations (AWS, OCP)

## Deployment
- **Periodic job:** `onboard config update` runs as a periodic Prow job for continuous config reconciliation across all managed clusters.
- **Manual invocation:** `provision` and `onboard config generate` are run interactively by engineers when standing up new clusters.
- Integration tests use the `CITOOLS_CLUSTERINIT_INTEGRATIONTEST` environment variable to toggle test-specific behavior.

## Quick-start usage

**Note:** This section documents the legacy CLI interface. The tool was refactored to use cobra subcommands (`provision`, `onboard config generate`, `onboard config update`). Consult `--help` for current usage.

### Create
In order to create a new build cluster the tool can be used like:
`cluster-init --release-repo=<path to local repo> --cluster-name=<new cluster name>`.

### Update
Updating existing build clusters to spec can be achieved by using the tool in update mode:
`cluster-init --release-repo=<path to local repo> --update=true`.
If it is desired to only update a single cluster, then `--cluster-name=<existing cluster name>` argument can be provided.

### Create PR
For either mode, if it is desired to create a new PR the `--create-pr=true` and `--github-token-path=<path to github auth token file>`
args will also need to be provided. If you would like the PR to be self-merging the `--self-approve=true` argument will also need to be provided.
