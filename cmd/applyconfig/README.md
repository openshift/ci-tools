# ApplyConfig

ApplyConfig is the GitOps tool used by Test Platform team to validate the kubernetes/OpenShift resource manifests stored
in [openshift/release](https://github.com/openshift/release) repository and maintain their presence in all clusters in
the OpenShift CI system.

## What it does

ApplyConfig is basically an `oc apply -f <file>` wrapper, behaving similarly to the `oc apply -f directory/ --recursive`
command (it recursively traverses the given directory and calls `oc apply -f` on every file it finds). In addition to
that, it adds several lightweight features required by Test Platform team, its workflows and conventions:

1. Allows non-resource YAML files to be present in the traversed directory
2. Ignores JSON files
3. Ignores directories and files that are marked by a convention, or ones that are explicitly exempted by a parameter
4. Processes and applies OpenShift templates and substitutes values read from environment variables as template
   parameters
5. Censors values read form environment variables in its output
6. While running in server-side dry mode, it handles the situation when both a namespace manifest and a manifest of a
   resource that would be placed in that namespace are not yet present in the cluster (in a normal dry run, applying the
   resource to a namespace that does not exist in the cluster would fail). It does so by bookkeeping which namespaces
   would be applied if not in dry run, and creating them temporarily if they do not exist.
7. Add `--server-side` to the `apply` command if a filename starts with `SS_`

## Why it exists

ApplyConfig is a simple GitOps tool that unifies the procedure to:

1. Validate the manifests in PRs to openshift/release before they are merged
2. Commit the manifests to the cluster after they are merged

## How it works

`applyconfig --config-dir DIRECTORY` searches for all resource config files under `DIRECTORY` and applies it.
Subdirectories are searched recursively and directories with names starting with `_` are skipped. Files and directories
are searched and applied in lexicographical order. All YAML files are considered to bXLe a config to apply, except those
with filenames starting with `_`.

By default, `applyconfig` only runs in dry-mode, validating that eventual full run would be successful. To issue a full
run that actually commits the config to the cluster, add a `--confirm=true` option.

The `--context=<context_name>` and `--kubeconfig=<kubeconfig_file>` options can be used to specify `<context_name>`
and `<kubeconfig_file>` respectively when executing `oc-apply` commands.

## How is it deployed

- The
  `pull-ci-openshift-release-master-$CLUSTER-dry` [presubmits](https://prow.ci.openshift.org/?repo=openshift%2Frelease&job=pull-ci-*-dry)
  execute on openshift/release PRs and validate changes to the manifests in the repository.
- The
  `branch-ci-openshift-release-master-$CLUSTER-apply` [postsubmits](https://prow.ci.openshift.org/?repo=openshift%2Frelease&job=branch-ci-*-apply)
  execute after merges to openshift/release and commits the manifests to the clusters
- The
  `periodic-openshift-release-master-$CLUSTER-apply` [periodics](https://prow.ci.openshift.org/?repo=openshift%2Frelease&job=periodic-*-apply)
  regularly re-commit the manifests to the form present in the repository, reverting manual changes even when there are
  no changes to the openshift/release repository

Additionally, the tool is exposed in
the [`Makefile` targets](https://github.com/openshift/release/blob/9acb52ff0cdb142df26a9416dd172ac525c4a17c/Makefile#L29-L39)
in the openshift/release repository for manual usage. This manual usage is likely obsolete and discouraged.
