The `multi-pr-prow-plugin` is an external prow plugin that facilitates running presubmit tests
from sources built using multiple pull requests. The included pull requests can be from the same, or a different, repo.
It creates and manages GitHub `check_runs` to keep share the state and logs of the jobs with the user.

## Usage

Comment on a PR as a trusted user:

```text
/testwith <org>/<repo>/<branch>/<variant?>/<test> [org/repo#123 ...]
```

Explicit PRs may also be GitHub URLs. The originating PR is always included.
Use `/testwith abort` to cancel active multi-PR jobs associated with the origin.

## Description dependencies

Declare dependencies anywhere in a PR description with case-insensitive
`Depends-On:` lines, using one PR reference per line:

```text
Depends-On: https://github.com/openshift/example/pull/123
Depends-On: openshift/another-repo#456
```

Markdown lists are also supported:

```markdown
Depends-On:
- https://github.com/openshift/example/pull/123
- openshift/another-repo#456
```

Or put each declaration in a bullet:

```markdown
- Depends-On: openshift/example#123
- Depends-On: openshift/another-repo#456
```

List markers `-`, `*`, and `+` and optional indentation are accepted. A blank
line may precede the first list item. Once the list starts, a blank line or a
non-list line ends it. Empty sections and malformed entries produce errors.
Declarations inside fenced code blocks are ignored.

With dependencies declared, explicit PR arguments are optional:

```text
/testwith openshift/my-repo/main/unit
```

The plugin recursively reads the descriptions of both the originating PR and
explicit PRs, includes each PR once, and orders dependencies before their
dependents. Cycles, inaccessible PRs, and closed, unmerged dependencies produce
errors. Merged description dependencies are treated as satisfied and are not
traversed; this assumes their changes are available on the tested branches.
Explicit PRs retain their existing behavior regardless of merged/closed state.

Discovery is limited to 20 unique PRs per job, including the origin and merged
dependencies, to bound GitHub requests. The existing limit of fewer than 20
explicit PR arguments also applies. If no unresolved dependencies remain, the
job tests the origin alone. The GitHub check lists included additional PRs.

The existing CI configuration and branch normalization requirements for
multi-PR testing still apply. Dependencies are read at invocation time: this
does not change `/test`, `/retest`, automatic presubmits, or merge gating, and
updates to dependency PRs do not automatically rerun tests.
