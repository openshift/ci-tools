# Publicize

This tool is an external prow plugin. Its purpose is to merge the commit histories of two repositories.

Configuration file example:
```yaml
repositories:
  openshift/private-repo: openshift/public-repo
  openshift/another-private-repo: openshift/another-public-repo
```


# Requirements

- Responds only in `/publicize` comments
- The plugin runs for only merged pull requests
- User must be an organization member
- The destination repository must be defined in the configuration file


# Workflow

- User comments `/publicize` to a merged pull request
- The plugin clones the destination repository
- The pull request's repository and the branch is being fetched
- Histories are being merged with a new commit message
- The branch is being pushed to the destination repository


