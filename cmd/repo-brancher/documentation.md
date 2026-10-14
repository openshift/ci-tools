# Repo brancher recovery

## Fast-forward failures

When the controller reports a failure in `--ops-channel-id` (normally
`#ops-testplatform`), inspect the error, branch histories, and forwarding
configuration before considering a reset.

1. Use the digest's compare link and commit IDs. Read the matching controller
   logs; `non_fast_forward` can also mean a protection or validation rejection.
2. Confirm the source and target should still be linked in the forwarding
   configuration. Fix incorrect mappings, authentication, or protection errors.
3. Inspect target-only commits in a full clone:

   ```bash
   git fetch origin
   git log --oneline origin/SOURCE..origin/TARGET
   git merge-base --is-ancestor origin/TARGET origin/SOURCE
   ```

   An ancestor check that exits 0 means a normal fast-forward is possible.
   Preserve legitimate target changes through the repository's normal process.

## Manual reset

After confirming with the repository owners that the target should match the
source and its target-only history can be discarded, use [reset-branch.sh](reset-branch.sh)
from the ci-tools checkout:

```bash
bash cmd/repo-brancher/reset-branch.sh \
  --repo OWNER/REPO --from SOURCE --to TARGET
```

Requires Bash, Git, `gh`, `jq`, `cmp`, and a repository administrator PAT able
to update refs and manage classic branch protection. The PAT is prompted
secretly; `GH_TOKEN` and `--pat` are also accepted. Without arguments, the script
prompts for the repository, source, target, and PAT.

The script requires typing the target name, creates a backup branch, saves
classic protection, pushes with `--force-with-lease`, then restores and verifies
protection. Cleanup attempts restoration after failures. It uses HTTPS/PAT
authentication even when global Git configuration rewrites GitHub URLs to SSH.
Repository rulesets need separate investigation with the administrator.

Keep the printed backup branch and snapshot. If protection restoration fails,
use the printed retry command or restore protection without resetting history:

```bash
bash cmd/repo-brancher/reset-branch.sh --restore-protection /path/to/snapshot.json
```

Verify the source and target commit IDs, backup at the old target commit, and
protection against the snapshot. Check the next reconciliation or periodic job
succeeds; the controller then clears the matching failure from future digests.
