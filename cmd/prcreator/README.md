# PRCreator

A simple CLI that will upsert a GitHub pr if the current working directory is in an unclean Git state.

## GitHub-signed commits

Pass `--github-signed-commits=true` together with `--pr-source-mode=branch`,
`--github-app-id`, and `--github-app-private-key-path` to opt into GitHub API
commit creation. The App needs Contents and Pull requests write permissions on
the target repository. Other callers retain the existing local commit/push flow.

This mode stages all changes and creates a commit using GraphQL
`createCommitOnBranch`, letting GitHub supply the author, committer, and
signature. It creates a temporary `prcreator-signing-*` branch from the local
HEAD, checks the returned signature, and then creates or replaces the PR branch.
Existing PR updates are regenerated from the checked-out base, as with the
current force-push flow. There is no fallback to an unsigned commit.

The API mode supports additions, modifications, deletions, and renames of
non-executable regular files, including binary contents. Executables, symlinks,
and submodules are rejected because this API cannot preserve their modes.
Temporary branches are deleted on success and failure; cleanup failures are
logged with the branch name for manual cleanup. Concurrent runs updating the
same PR branch retain the existing last-writer-wins behavior.

Roll out to one job first and verify both initial PR creation and updates on
GitHub before enabling other callers. Use a test repository to validate a full
merge with required signatures; unit tests cannot establish GitHub's live
signing behavior.
