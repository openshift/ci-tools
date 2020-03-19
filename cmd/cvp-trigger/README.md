# CVP Trigger

CVP Trigger tool will be used by the CVP pipeline to parametrize and trigger
the verification jobs for optional operator artifacts built internally in RH.

## Triggered Job Interface

The tool should work by loading Prow and Job configuration, finding specific job
configuration structs and modifying those structs so that their specified
containers are executed with the specified environmental variables having the
desired values.

### Common CVP Tests for Optional Operators

CVP Trigger must load the configuration for the
`cpaas-cvp-optional-operator-common-tests` Prowjob and modify its
`.Spec.Containers[0].Env` list to have the following environmental variables set:

- `OCP_VERSION`: Required. A string specifying the desired minor OCP version
  which will be used when provisioning the ephemeral testing cluster. If
  `RELEASE_IMAGE_LATEST` is not specified, an ephemeral release payload will be
  created, corresponding to the collection of OCP images built from HEADs of
  appropriate release branches of OCP components (that is, latest development
  version of that minor release).
- `CLUSTER_TYPE`: Required. One of `aws` strings, specifying the cloud platform
  where the ephemeral cluster will be created.
- `RELEASE_IMAGE_LATEST`: Optional. A full pullspec of a release payload image
  which will be used when provisioning the ephemeral testing cluster.
