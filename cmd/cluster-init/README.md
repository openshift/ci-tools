# Cluster Init
`cluster-init` is a tool for creating and managing build clusters. It generates and updates yaml configurations for the clusters in the `openshift/release` repo, and, if desired, will create a self-merging PR for these configurations. This tool operates in one of two modes:

## Create
In order to create a new build cluster the tool can be used like:
`cluster-init --release-repo=<path to local repo> --cluster-name=<new cluster name>`.


## Update
Updating existing build clusters to spec can be achieved by using the tool in update mode:
`cluster-init --release-repo=<path to local repo> --update=true`.
If it is desired to only update a single cluster, then `--cluster-name=<existing cluster name>` argument can be provided.

## Create PR
For either mode, if it is desired to create a new PR the `--create-pr=true` and `--github-token-path=<path to github auth token file>`
args will also need to be provided. If you would like the PR to be self-merging the `--self-approve=true` argument will also need to be provided.