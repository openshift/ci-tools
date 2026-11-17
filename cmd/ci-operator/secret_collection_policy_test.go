package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/group"
)

// policyTestStep exposes only the metadata preflight may inspect.
type policyTestStep struct {
	api.Step
	name string
}

// Name returns the selected test target.
func (s policyTestStep) Name() string { return s.name }

// TestCheckBlockedCollections covers distinct sources without executing steps or discovering fields.
func TestCheckBlockedCollections(t *testing.T) {
	fallback := "test selected: The secret collection blocked is blocked. Contact <#CBN38N3MW> to get more details."
	for _, tc := range []struct {
		name, reason, want string
		secret             *api.Secret
		literal            *api.MultiStageTestConfigurationLiteral
		unselected         bool
	}{
		{name: "explicit field", secret: &api.Secret{Collection: "blocked", Group: "group", Field: "field"}, want: fallback},
		{name: "auto discovery and custom reason", secret: &api.Secret{Collection: "blocked", Group: "group"}, reason: "maintenance", want: "test selected: secret collection blocked is blocked: maintenance"},
		{name: "allowed direct", secret: &api.Secret{Collection: "allowed"}},
		{name: "unselected", secret: &api.Secret{Collection: "blocked"}, unselected: true},
		{name: "bundle", secret: &api.Secret{Bundle: "bundle"}, want: fallback},
		{name: "legacy synced secret", secret: &api.Secret{Name: "synced", Namespace: "ci"}, want: fallback},
		{name: "different namespace", secret: &api.Secret{Name: "synced", Namespace: "other"}},
		{name: "pre", literal: &api.MultiStageTestConfigurationLiteral{Pre: []api.LiteralTestStep{{Credentials: []api.CredentialReference{{Collection: "blocked"}}}}}, want: fallback},
		{name: "test", literal: &api.MultiStageTestConfigurationLiteral{Test: []api.LiteralTestStep{{Credentials: []api.CredentialReference{{Collection: "blocked"}}}}}, want: fallback},
		{name: "post", literal: &api.MultiStageTestConfigurationLiteral{Post: []api.LiteralTestStep{{Credentials: []api.CredentialReference{{Collection: "blocked"}}}}}, want: fallback},
		{name: "profile", literal: &api.MultiStageTestConfigurationLiteral{ClusterProfileLiteral: &api.ClusterProfileLiteral{Secret: "synced"}}, want: fallback},
		{name: "dockerconfig", secret: &api.Secret{Bundle: "docker"}, want: fallback},
		{name: "allowed bundle", secret: &api.Secret{Bundle: "allowed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := options{
				configSpec:             &api.ReleaseBuildConfiguration{Tests: []api.TestStepConfiguration{{As: "selected", Secrets: []*api.Secret{tc.secret}, MultiStageTestConfigurationLiteral: tc.literal}}},
				secretCollectionPolicy: group.Config{BlockedCollections: map[string]group.BlockedCollection{"blocked": {Reason: tc.reason}}},
				gsmConfig: api.GSMConfig{DPTPCollection: "blocked", Bundles: []api.GSMBundle{
					{Name: "bundle", GSMSecrets: []api.GSMSecretRef{{Collection: "blocked"}}},
					{Name: "synced", SyncToCluster: true, Targets: []api.TargetSpec{{Namespace: "ci"}}, GSMSecrets: []api.GSMSecretRef{{Collection: "blocked"}}},
					{Name: "docker", DockerConfig: &api.DockerConfigSpec{}},
					{Name: "allowed", GSMSecrets: []api.GSMSecretRef{{Collection: "allowed"}}},
				}},
			}
			selected := "selected"
			if tc.unselected {
				selected = "other"
			}
			got := ""
			if err := o.checkBlockedCollections(api.OrderedStepList{{Step: policyTestStep{name: selected}}}); err != nil {
				got = err.Error()
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("unexpected error (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLoadSecretCollectionPolicy distinguishes optional policy from required active-policy bundle metadata.
func TestLoadSecretCollectionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, data, metadata, wantError        string
		defaultPolicy, defaultBundle, disabled bool
		want                                   map[string]group.BlockedCollection
	}{
		{name: "disabled", disabled: true},
		{name: "absent", data: "groups: {}"},
		{name: "null", data: "blocked_collections: null"},
		{name: "empty", data: "blocked_collections: {}", want: map[string]group.BlockedCollection{}},
		{name: "reason forms", data: "blocked_collections: {omitted: {}, null-entry: null, empty: {reason: ''}, null-reason: {reason: null}, custom: {reason: maintenance}}", metadata: "{}", want: map[string]group.BlockedCollection{"omitted": {}, "null-entry": {}, "empty": {}, "null-reason": {}, "custom": {Reason: "maintenance"}}},
		{name: "missing default bundle metadata", data: "blocked_collections: {blocked: {}}", defaultBundle: true, wantError: "load bundle metadata for secret collection policy:"},
		{name: "missing explicit bundle metadata", data: "blocked_collections: {blocked: {}}", wantError: "load bundle metadata for secret collection policy:"},
		{name: "unknown field", data: "blocked_collections: {blocked: {typo: reason}}", wantError: "parse secret collection policy:"},
		{name: "explicit missing policy", wantError: "read secret collection policy:"},
		{name: "missing default policy", defaultPolicy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := options{secretCollectionPolicyPath: filepath.Join(t.TempDir(), "ownership.yaml"), gsmConfigPath: filepath.Join(t.TempDir(), "gsm-config.yaml")}
			if tc.defaultPolicy {
				o.secretCollectionPolicyPath = api.GSMConfigMountPath + "/ownership.yaml"
			}
			if tc.disabled {
				o.secretCollectionPolicyPath = ""
			}
			if tc.defaultBundle {
				o.gsmConfigPath = ""
			}
			for path, data := range map[string]string{o.secretCollectionPolicyPath: tc.data, o.gsmConfigPath: tc.metadata} {
				if data != "" {
					if err := os.WriteFile(path, []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := o.loadSecretCollectionPolicy(); err != nil {
				if tc.wantError == "" || !strings.HasPrefix(err.Error(), tc.wantError) || (tc.defaultBundle && !errors.Is(err, os.ErrNotExist)) {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if tc.wantError != "" {
				t.Fatalf("expected error starting with %q", tc.wantError)
			}
			if diff := cmp.Diff(tc.want, o.secretCollectionPolicy.BlockedCollections); diff != "" {
				t.Errorf("unexpected policy (-want +got):\n%s", diff)
			}
			if len(tc.want) == 0 {
				if err := o.checkBlockedCollections(nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
