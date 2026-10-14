# PRCreator

A simple CLI that will upsert a GitHub pr if the current working directory is in an unclean Git state.

## GitHub-signed commits

Pass `--github-signed-commits=true` together with `--pr-source-mode=branch`,
`--github-app-id`, and `--github-app-private-key-path` to create GitHub-signed
commits. The App needs Contents and Pull requests write permissions on the
target repository. This mode supports non-executable regular files, including
binary contents, and rejects executables, symlinks, and submodules. If GitHub
does not return a valid signature, the PR branch is left unchanged.
