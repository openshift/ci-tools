package main

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

// loadGSMConfig loads the supplied source even without CSI, leaving legacy jobs without a path unchanged.
func (o *options) loadGSMConfig() error {
	if o.gsmConfigPath == "" && !o.enableSecretsStoreCSIDriver {
		return nil
	}
	return api.LoadGSMConfigFromFile(o.gsmConfigPath, &o.gsmConfig)
}

// checkBlockedCollections checks only tests present in the selected execution graph.
func (o *options) checkBlockedCollections(steps api.OrderedStepList) error {
	if len(o.gsmConfig.BlockedCollections) == 0 {
		return nil
	}
	selected := sets.New[string]()
	for _, step := range steps {
		selected.Insert(step.Step.Name())
	}
	for _, test := range o.configSpec.Tests {
		if !selected.Has(test.As) {
			continue
		}
		var credentials []api.CredentialReference
		secrets := append([]*api.Secret{test.Secret}, test.Secrets...)
		for _, secret := range secrets {
			if secret != nil {
				credentials = append(credentials, api.CredentialReference{Collection: secret.Collection, Bundle: secret.Bundle, Namespace: secret.Namespace, Name: secret.Name})
			}
		}
		if literal := test.MultiStageTestConfigurationLiteral; literal != nil {
			for _, stage := range [][]api.LiteralTestStep{literal.Pre, literal.Test, literal.Post} {
				for _, step := range stage {
					credentials = append(credentials, step.Credentials...)
				}
			}
			if profile := literal.ClusterProfileLiteral; profile != nil {
				credentials = append(credentials, api.CredentialReference{Namespace: "ci", Name: profile.Secret})
			}
		}
		for _, credential := range credentials {
			if err := o.checkCredentialCollections(credential); err != nil {
				return fmt.Errorf("test %s: %w", test.As, err)
			}
		}
	}
	return nil
}

// checkCredentialCollections inspects bundle sources before any sync-to-cluster conversion.
func (o *options) checkCredentialCollections(credential api.CredentialReference) error {
	collections := sets.New[string](credential.Collection)
	for _, bundle := range o.gsmConfig.Bundles {
		matches := credential.Bundle != "" && credential.Bundle == bundle.Name
		if credential.Name != "" && credential.Name == bundle.Name && bundle.SyncToCluster {
			for _, target := range bundle.Targets {
				if target.Namespace == credential.Namespace {
					matches = true
				}
			}
		}
		if !matches {
			continue
		}
		for _, secret := range bundle.GSMSecrets {
			collections.Insert(secret.Collection)
		}
		if bundle.DockerConfig != nil {
			collections.Insert(o.gsmConfig.DPTPCollection)
		}
	}
	for _, collection := range sets.List(collections) {
		if block, found := o.gsmConfig.BlockedCollections[collection]; found {
			reason := block.Reason
			if reason == "" {
				reason = fmt.Sprintf("The secret collection %s is blocked. Contact <#CBN38N3MW> to get more details.", collection)
				return errors.New(reason)
			}
			return fmt.Errorf("secret collection %s is blocked: %s", collection, reason)
		}
	}
	return nil
}
