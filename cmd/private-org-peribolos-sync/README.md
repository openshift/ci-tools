# Private org peribolos sync

This tool generates a mapping table of peribolos repository configurations for the given private
organization. 
It walks through the release repository path, given by `--release-repo-path`, and detects which of the repositories
are promoting official images. The repositories that are specified in `--include-repo`, will be included if they exist in the release repository's path as well. Furthermore, it will get the required information for each of them from GitHub
and will generate its peribolos configuration.
Finally, the tool will update the peribolos configuration given by `--peribolos-config`.

## Repository Naming Convention

To prevent collisions when multiple organizations have repositories with the same name, this tool uses a special naming convention:
- Repositories from the organization specified by `--only-org` keep their original names
- Repositories from organizations specified by `--flatten-org` keep their original names (can be specified multiple times)
- Repositories from the following default organizations always keep their original names for backwards compatibility:
  - `openshift`
  - `openshift-eng`
  - `operator-framework`
  - `redhat-cne`
  - `openshift-assisted`
  - `ViaQ`
- All other repositories are named as `<org>-<repo>`

For example, with `--only-org=openshift --flatten-org=migtools`:
- `openshift/must-gather` → `openshift-priv/must-gather` (from --only-org and default)
- `openshift-eng/ocp-build-data` → `openshift-priv/ocp-build-data` (from default)
- `migtools/crane` → `openshift-priv/crane` (from --flatten-org)
- `redhat-cne/cloud-event-proxy` → `openshift-priv/cloud-event-proxy` (from default)
- `custom-org/some-repo` → `openshift-priv/custom-org-some-repo` (not in flatten list)
