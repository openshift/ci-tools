package csi_secrets

import (
	secretmanager "cloud.google.com/go/secretmanager/apiv1"

	"github.com/openshift/ci-tools/pkg/api"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
)

// GSMConfiguration contains all Google Secret Manager (GSM) related configuration
// needed for test execution (both multi-stage and container tests).
type GSMConfiguration struct {
	Config          *api.GSMConfig
	CredentialsFile string
	Client          *secretmanager.Client
	ProjectConfig   gsm.Config
}

// CollectionGroupKey identifies a unique (collection, group) pair for caching discovered fields.
type CollectionGroupKey struct {
	Collection string
	Group      string
}
