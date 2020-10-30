# Template Deprecator

This tool maintains the allowlist that drives the template deprecation
(see https://docs.ci.openshift.org/docs/how-tos/migrating-template-jobs-to-multistage/ 
for more information) and enforces the current desired state of the repository.

The tool loads the current allowlist first. Then it processes the Prow configuration
and detects all jobs that use a test template, updating the allowlist during the
process. The tool validates the changed allowlist and fails if it contains some
undesirable item, like a job that uses a fully deprecated template or other
configuration that should simply not be added to the repository.

If no undesirable configuration is detected, the tool saves the modified allowlist
to the original location.
