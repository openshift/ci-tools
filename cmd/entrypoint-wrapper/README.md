# entrypoint-wrapper

## What
Wraps every multi-stage test step executed by ci-operator. Manages secret copying to writable temp locations, kubeconfig isolation with polling fallback, HOME directory fixups, Git safe directory config, signal forwarding to child processes, and post-execution artifact upload as Kubernetes Secrets.

Not a standalone service. ci-operator injects this binary as the entrypoint of every test step container.

## How it works — full flow

### 1. Parse and validate
- Reads required env vars: `SHARED_DIR`, `NAMESPACE`, `JOB_NAME_SAFE`
- Validates mode flag (one of three modes below)
- Creates Kubernetes client unless dry-run or skip-kubeconfig mode

### 2. Copy SHARED_DIR to writable temp
`copyDir()` copies files from the mounted `SHARED_DIR` to `$TMPDIR/secret`. This is a **non-recursive, top-level-only copy** — subdirectories are skipped. The `SHARED_DIR` env var is then updated to point to the temp copy. This is how steps pass data to subsequent steps: each step reads the previous step's shared dir snapshot from a mounted Secret, writes to the temp copy, and the wrapper uploads it back.

### 3. Wait for file (optional)
If `--wait-for-file` is set, blocks until the file appears (event-driven filesystem watching via fsnotify). Used for observer pods that need to wait for cluster install to complete. `--wait-timeout` caps how long to wait.

### 4. Spawn kubeconfig upload goroutine
If `uploadKubeconfig` is true, starts a background goroutine that polls for `kubeconfig` (and `kubeconfig-minimal`) in the shared dir and uploads them as Kubernetes Secrets. Uses `wait.PollUntil()` with 1-second intervals. This runs concurrently with the test — observers can read the kubeconfig as soon as it appears.

### 5. Set up environment and execute child process
Before exec'ing the actual test command:

- **HOME fixup** (`manageHome()`): If `HOME` is unset or not writable (checked via `syscall.Access`), sets `HOME=/alabama`. This ensures kubectl/oc can write discovery cache. The `/alabama` path is a deliberate magic constant.

- **Git config** (`manageGitConfig()`): Creates `$HOME/.gitconfig` with `[safe] directory = *` if it doesn't already exist. This disables Git's ownership verification — necessary because build containers run with different UIDs than the mounted repo.

- **CLI_DIR in PATH**: If `CLI_DIR` env var is set, appends it to `PATH` so tools installed by previous steps are available.

- **Kubeconfig isolation** (`manageKubeconfig()`): Creates a temp file copy of the original `KUBECONFIG`. If the original doesn't exist yet (common for observer pods where kubeconfig arrives later), starts a background polling goroutine using `wait.PollImmediateInfinite(time.Second)` that copies the kubeconfig as soon as it appears. Updates `KUBECONFIG` env var to point to the isolated copy.

- **Signal forwarding**: Registers handlers for `SIGINT` and `SIGTERM`, forwards them to the child process. Runs in a goroutine that exits when the child process completes.

- **Exec**: Starts the child command with the modified environment, waits for completion.

### 6. Cleanup and upload
After the child exits:
- Cancels the kubeconfig upload goroutine
- If `updateSharedDir` is true: reads all files from the temp shared dir via `util.SecretFromDir()` (skips directories, broken symlinks, symlinks to directories) and updates a Kubernetes Secret named after `JOB_NAME_SAFE`
- Returns the child's exit code

## Three modes

| Mode | Flag value | uploadKubeconfig | updateSharedDir | rwKubeconfig | Use case |
|---|---|---|---|---|---|
| **manage-kubeconfig** | `manage-kubeconfig` (default) | true | true | true | Normal test steps |
| **skip-kubeconfig** | `skip-kubeconfig` | false | false | false | Steps that don't need cluster access |
| **observer** | `observer` | false | false | true | Observer pods — read-only kubeconfig, no upload |

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--mode` | `manage-kubeconfig` | Kubeconfig management mode (see table above) |
| `--dry-run` | `false` | Print the secret instead of creating it |
| `--wait-for-file` | `""` | Path to file to wait for before starting the child |
| `--wait-timeout` | `""` | Max wait duration; requires `--wait-for-file` |

## Key files
- `cmd/entrypoint-wrapper/main.go` — all logic in one file (~480 lines)
- `pkg/util/secret.go` — `SecretFromDir()` reads directory contents into a k8s Secret

## Deployment
Not independently deployed. Injected into every multi-stage test step container by ci-operator.

Container image: built from `images/entrypoint-wrapper/Dockerfile`, base `ubi9/ubi-minimal`.
