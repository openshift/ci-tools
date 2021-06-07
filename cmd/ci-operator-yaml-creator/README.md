# CI-Operator-Yaml Creator

A small tool that will create a PullRequest with a `.ci-operator.yaml` file for the `main`/`master` branch of all repositories
that are built by ART, don't have `build_root_image.from_repository: true` and where there is currently no `.ci-operator.yaml`
file matching the `build_root` configured in openshift/release.

If the `.ci-operator.yaml` is already up-to-date, it will set `build_root.from_repository: true`
