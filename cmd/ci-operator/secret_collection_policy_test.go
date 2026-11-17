package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
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
				configSpec: &api.ReleaseBuildConfiguration{Tests: []api.TestStepConfiguration{{As: "selected", Secrets: []*api.Secret{tc.secret}, MultiStageTestConfigurationLiteral: tc.literal}}},
				gsmConfig: api.GSMConfig{BlockedCollections: map[string]api.BlockedCollection{"blocked": {Reason: tc.reason}}, DPTPCollection: "blocked", Bundles: []api.GSMBundle{
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

// TestLoadGSMConfig covers legacy omission and explicit single-source loading without CSI.
func TestLoadGSMConfig(t *testing.T) {
	for _, tc := range []struct {
		name, data             string
		noPath, csi, wantError bool
		want                   map[string]api.BlockedCollection
	}{
		{name: "legacy no path", noPath: true},
		{name: "CSI requires config", noPath: true, csi: true, wantError: true},
		{name: "explicit missing source", wantError: true},
		{name: "malformed source", data: "bundles: [", wantError: true},
		{name: "absent policy", data: "{}"},
		{name: "null policy", data: "blocked_collections: null"},
		{name: "empty policy", data: "blocked_collections: {}", want: map[string]api.BlockedCollection{}},
		{name: "reason forms", data: "blocked_collections: {omitted: {}, null-entry: null, empty: {reason: ''}, null-reason: {reason: null}, custom: {reason: maintenance}}", want: map[string]api.BlockedCollection{"omitted": {}, "null-entry": {}, "empty": {}, "null-reason": {}, "custom": {Reason: "maintenance"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := options{gsmConfigPath: filepath.Join(t.TempDir(), "gsm-config.yaml"), enableSecretsStoreCSIDriver: tc.csi}
			if tc.noPath {
				o.gsmConfigPath = ""
			}
			if tc.data != "" {
				if err := os.WriteFile(o.gsmConfigPath, []byte(tc.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := o.loadGSMConfig(); (err != nil) != tc.wantError {
				t.Fatalf("loadGSMConfig() = %v, wantError %v", err, tc.wantError)
			}
			if diff := cmp.Diff(tc.want, o.gsmConfig.BlockedCollections); diff != "" {
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
