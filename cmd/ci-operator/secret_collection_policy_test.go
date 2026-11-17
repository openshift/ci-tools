package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/group"
)

// policyTestStep exposes graph metadata but panics if preflight attempts execution or I/O.
type policyTestStep struct {
	api.Step
	name string
}

// Name returns the selected test target.
func (s policyTestStep) Name() string { return s.name }

// Requires returns the inputs needed to select this metadata-only test target.
func (s policyTestStep) Requires() []api.StepLink { return nil }

// Creates returns outputs of this metadata-only test target.
func (s policyTestStep) Creates() []api.StepLink { return nil }

// TestCheckBlockedCollections verifies metadata-only checking of the selected graph.
func TestCheckBlockedCollections(t *testing.T) {
	fallback := "test selected: The secret collection blocked is blocked. Contact <#CBN38N3MW> to get more details."
	tests := []struct {
		name       string
		credential api.CredentialReference
		stage      string
		selected   string
		reason     string
		want       string
	}{
		{name: "explicit field", credential: api.CredentialReference{Collection: "blocked", Group: "group", Field: "field"}, want: fallback},
		{name: "auto discovery", credential: api.CredentialReference{Collection: "blocked", Group: "group"}, want: fallback},
		{name: "custom reason", credential: api.CredentialReference{Collection: "blocked"}, reason: "collection is under investigation", want: "test selected: secret collection blocked is blocked: collection is under investigation"},
		{name: "unblocked", credential: api.CredentialReference{Collection: "allowed"}},
		{name: "unblocked explicit field", credential: api.CredentialReference{Collection: "allowed", Group: "group", Field: "field"}},
		{name: "unblocked auto discovery", credential: api.CredentialReference{Collection: "allowed", Group: "group"}},
		{name: "unselected", credential: api.CredentialReference{Collection: "blocked"}, selected: "other"},
		{name: "bundle", credential: api.CredentialReference{Bundle: "bundle"}, want: fallback},
		{name: "synced bundle", credential: api.CredentialReference{Bundle: "synced", Namespace: "ci"}, want: fallback},
		{name: "legacy synced secret", credential: api.CredentialReference{Name: "synced", Namespace: "ci"}, want: fallback},
		{name: "different namespace", credential: api.CredentialReference{Name: "synced", Namespace: "other"}},
		{name: "pre", credential: api.CredentialReference{Collection: "blocked"}, stage: "pre", want: fallback},
		{name: "test", credential: api.CredentialReference{Collection: "blocked"}, stage: "test", want: fallback},
		{name: "post", credential: api.CredentialReference{Collection: "blocked"}, stage: "post", want: fallback},
		{name: "profile", stage: "profile", want: fallback},
		{name: "dockerconfig", credential: api.CredentialReference{Bundle: "docker"}, want: fallback},
		{name: "allowed bundle", credential: api.CredentialReference{Bundle: "allowed"}},
		{name: "allowed synced bundle", credential: api.CredentialReference{Bundle: "allowed-synced", Namespace: "ci"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selected := "selected"
			if tc.selected != "" {
				selected = tc.selected
			}
			test := api.TestStepConfiguration{As: "selected"}
			literal := &api.MultiStageTestConfigurationLiteral{}
			step := api.LiteralTestStep{Credentials: []api.CredentialReference{tc.credential}}
			switch tc.stage {
			case "pre":
				literal.Pre = []api.LiteralTestStep{step}
			case "test":
				literal.Test = []api.LiteralTestStep{step}
			case "post":
				literal.Post = []api.LiteralTestStep{step}
			case "profile":
				literal.ClusterProfileLiteral = &api.ClusterProfileLiteral{Secret: "synced"}
			default:
				test.Secrets = []*api.Secret{{Collection: tc.credential.Collection, Bundle: tc.credential.Bundle, Namespace: tc.credential.Namespace, Name: tc.credential.Name, Group: tc.credential.Group, Field: tc.credential.Field}}
			}
			test.MultiStageTestConfigurationLiteral = literal
			o := options{
				configSpec:             &api.ReleaseBuildConfiguration{Tests: []api.TestStepConfiguration{test}},
				secretCollectionPolicy: group.Config{BlockedCollections: map[string]group.BlockedCollection{"blocked": {Reason: tc.reason}}},
				gsmConfig: api.GSMConfig{DPTPCollection: "blocked", Bundles: []api.GSMBundle{
					{Name: "bundle", GSMSecrets: []api.GSMSecretRef{{Collection: "blocked"}}},
					{Name: "synced", SyncToCluster: true, Targets: []api.TargetSpec{{Namespace: "ci"}}, GSMSecrets: []api.GSMSecretRef{{Collection: "blocked"}}},
					{Name: "docker", DockerConfig: &api.DockerConfigSpec{}},
					{Name: "allowed", GSMSecrets: []api.GSMSecretRef{{Collection: "allowed"}}},
					{Name: "allowed-synced", SyncToCluster: true, Targets: []api.TargetSpec{{Namespace: "ci"}}, GSMSecrets: []api.GSMSecretRef{{Collection: "allowed"}}},
				}},
			}
			nodes, err := api.BuildPartialGraph([]api.Step{policyTestStep{name: "selected"}, policyTestStep{name: "other"}}, []string{selected})
			if err != nil {
				t.Fatal(err)
			}
			ordered, sortErrors := nodes.TopologicalSort()
			if len(sortErrors) != 0 {
				t.Fatal(sortErrors)
			}
			err = o.checkBlockedCollections(ordered)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("unexpected error (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLoadSecretCollectionPolicy verifies backward-compatible optional metadata loading.
func TestLoadSecretCollectionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, data             string
		wantBlocked, wantError bool
	}{
		{name: "absent", data: "groups: {}"},
		{name: "null", data: "blocked_collections: null"},
		{name: "empty", data: "blocked_collections: {}"},
		{name: "empty metadata", data: "blocked_collections: {blocked: {}}", wantBlocked: true},
		{name: "null metadata", data: "blocked_collections: {blocked: null}", wantBlocked: true},
		{name: "empty reason", data: "blocked_collections: {blocked: {reason: ''}}", wantBlocked: true},
		{name: "null reason", data: "blocked_collections: {blocked: {reason: null}}", wantBlocked: true},
		{name: "reason", data: "blocked_collections: {blocked: {reason: maintenance}}", wantBlocked: true},
		{name: "invalid", data: "blocked_collections: [blocked]", wantError: true},
		{name: "unknown field", data: "blocked_collections: {blocked: {typo: reason}}", wantError: true},
		{name: "explicit missing", wantError: true},
		{name: "missing default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ownership.yaml")
			if tc.name == "missing default" {
				path = api.GSMConfigMountPath + "/ownership.yaml"
			}
			if tc.data != "" {
				if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			o := options{secretCollectionPolicyPath: path}
			err := o.loadSecretCollectionPolicy()
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantError {
				return
			}
			_, blocked := o.secretCollectionPolicy.BlockedCollections["blocked"]
			if diff := cmp.Diff(tc.wantBlocked, blocked); diff != "" {
				t.Errorf("unexpected policy (-want +got):\n%s", diff)
			}
			if !tc.wantBlocked && !tc.wantError {
				if err := o.checkBlockedCollections(nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
