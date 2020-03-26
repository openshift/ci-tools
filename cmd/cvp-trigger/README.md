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
`.Spec.Containers[0].Env` list to have the following environmental variables set.

#### Parameters specifying OCP cluster

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

#### Parameters specifying optional operator to be installed on OCP cluster

- `OO_INDEX`: Required. A pullspec of the the index image.
- `OO_PACKAGE`: Required. The name of the operator package to be installed. Must
   be present in the index image referenced by `$OO_INDEX`.
- `OO_CHANNEL`: Required. The name of the operator channel to track.
- `OO_INSTALL_NAMESPACE`: Optional. The namespace into which the operator and
   catalog will be installed. If empty, a new namespace will be created.
- `OO_TARGET_NAMESPACES`: Optional. A comma-separated list of namespaces the
   operator will target. If empty, all namespaces will be targeted.
   If no OperatorGroup exists in `$OO_INSTALL_NAMESPACE`, a new one will be
   created with its target namespaces set to `$OO_TARGET_NAMESPACES`, otherwise
   the existing OperatorGroup's target namespace set will be replaced. The special
   value "!install" will set the target namespace to the operator's installation
   namespace.
