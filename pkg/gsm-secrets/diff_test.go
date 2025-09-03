package gsmsecrets

import (
	"bytes"
	"fmt"
	"testing"

	"cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/genproto/googleapis/type/expr"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestDiffServiceAccounts(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}
	makeServiceAccount := func(collection string) ServiceAccountInfo {
		return ServiceAccountInfo{
			Email:       GetUpdaterSAEmail(collection, config),
			DisplayName: GetUpdaterSAId(collection),
			Collection:  collection,
		}
	}

	serviceAccountAlpha := makeServiceAccount("alpha")
	serviceAccountBeta := makeServiceAccount("beta")
	serviceAccountGamma := makeServiceAccount("gamma")
	serviceAccountDelta := makeServiceAccount("delta")
	serviceAccountEpsilon := makeServiceAccount("epsilon")
	serviceAccountZeta := makeServiceAccount("zeta")
	serviceAccountProd := makeServiceAccount("prod")
	serviceAccountStaging := makeServiceAccount("staging")
	serviceAccountDev := makeServiceAccount("dev")
	serviceAccountLegacy := makeServiceAccount("legacy")
	serviceAccountTemp := makeServiceAccount("temp")

	testCases := []struct {
		name             string
		desiredSAs       []ServiceAccountInfo
		actualSAs        []ServiceAccountInfo
		expectedToCreate SAMap
		expectedToDelete SAMap
	}{
		{
			name: "one new SA to create",
			desiredSAs: []ServiceAccountInfo{
				serviceAccountAlpha,
			},
			actualSAs: []ServiceAccountInfo{},
			expectedToCreate: SAMap{
				GetUpdaterSAEmail("alpha", config): serviceAccountAlpha,
			},
			expectedToDelete: SAMap{},
		},
		{
			name:       "one SA to delete",
			desiredSAs: []ServiceAccountInfo{},
			actualSAs: []ServiceAccountInfo{
				serviceAccountAlpha,
			},
			expectedToCreate: SAMap{},
			expectedToDelete: SAMap{
				GetUpdaterSAEmail("alpha", config): serviceAccountAlpha,
			},
		},
		{
			name: "no diff",
			desiredSAs: []ServiceAccountInfo{
				serviceAccountAlpha,
				serviceAccountBeta,
			},
			actualSAs: []ServiceAccountInfo{
				serviceAccountAlpha,
				serviceAccountBeta,
			},
			expectedToCreate: SAMap{},
			expectedToDelete: SAMap{},
		},
		{
			name: "simultaneous create and delete operations",
			desiredSAs: []ServiceAccountInfo{
				serviceAccountAlpha, // keep existing
				serviceAccountGamma, // new
			},
			actualSAs: []ServiceAccountInfo{
				serviceAccountAlpha, // keep this
				serviceAccountBeta,  // delete this
			},
			expectedToCreate: SAMap{
				GetUpdaterSAEmail("gamma", config): serviceAccountGamma,
			},
			expectedToDelete: SAMap{
				GetUpdaterSAEmail("beta", config): serviceAccountBeta,
			},
		},
		{
			name: "large scale operations",
			desiredSAs: []ServiceAccountInfo{
				serviceAccountProd,
				serviceAccountStaging,
				serviceAccountDev,
				serviceAccountAlpha, // keep existing
			},
			actualSAs: []ServiceAccountInfo{
				serviceAccountAlpha,  // keep
				serviceAccountBeta,   // delete
				serviceAccountLegacy, // delete
				serviceAccountTemp,   // delete
			},
			expectedToCreate: SAMap{
				GetUpdaterSAEmail("prod", config):    serviceAccountProd,
				GetUpdaterSAEmail("staging", config): serviceAccountStaging,
				GetUpdaterSAEmail("dev", config):     serviceAccountDev,
			},
			expectedToDelete: SAMap{
				GetUpdaterSAEmail("beta", config):   serviceAccountBeta,
				GetUpdaterSAEmail("legacy", config): serviceAccountLegacy,
				GetUpdaterSAEmail("temp", config):   serviceAccountTemp,
			},
		},
		{
			name: "partial config update - some collections added",
			desiredSAs: []ServiceAccountInfo{
				serviceAccountAlpha, // existing
				serviceAccountBeta,  // existing
				serviceAccountDelta,
				serviceAccountEpsilon,
			},
			actualSAs: []ServiceAccountInfo{
				serviceAccountAlpha,
				serviceAccountBeta,
			},
			expectedToCreate: SAMap{
				GetUpdaterSAEmail("delta", config):   serviceAccountDelta,
				GetUpdaterSAEmail("epsilon", config): serviceAccountEpsilon,
			},
			expectedToDelete: SAMap{},
		},
		{
			name:       "complete teardown scenario",
			desiredSAs: []ServiceAccountInfo{},
			actualSAs: []ServiceAccountInfo{
				serviceAccountAlpha,
				serviceAccountBeta,
				serviceAccountLegacy,
			},
			expectedToCreate: SAMap{},
			expectedToDelete: SAMap{
				GetUpdaterSAEmail("alpha", config):  serviceAccountAlpha,
				GetUpdaterSAEmail("beta", config):   serviceAccountBeta,
				GetUpdaterSAEmail("legacy", config): serviceAccountLegacy,
			},
		},
		{
			name: "bootstrap scenario - first time setup",
			desiredSAs: []ServiceAccountInfo{
				serviceAccountAlpha,
				serviceAccountBeta,
				serviceAccountZeta,
			},
			actualSAs: []ServiceAccountInfo{}, // no existing SAs
			expectedToCreate: SAMap{
				GetUpdaterSAEmail("alpha", config): serviceAccountAlpha,
				GetUpdaterSAEmail("beta", config):  serviceAccountBeta,
				GetUpdaterSAEmail("zeta", config):  serviceAccountZeta,
			},
			expectedToDelete: SAMap{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			toCreate, toDelete := DiffServiceAccounts(tc.desiredSAs, tc.actualSAs)
			testhelper.Diff(t, "toCreate", toCreate, tc.expectedToCreate)
			testhelper.Diff(t, "toDelete", toDelete, tc.expectedToDelete)
		})
	}
}

func TestDiffSecrets(t *testing.T) {
	testCollection := "test-collection"
	SAsecret := GCPSecret{
		Name:       GetUpdaterSASecretName(testCollection),
		Type:       SecretTypeSA,
		Collection: testCollection,
	}
	indexSecret := GCPSecret{
		Name:       GetIndexSecretName(testCollection),
		Type:       SecretTypeIndex,
		Collection: testCollection,
	}

	testCases := []struct {
		name               string
		desiredSecrets     map[string]GCPSecret
		actualSecrets      map[string]GCPSecret
		desiredCollections map[string]bool
		expectedToCreate   map[string]GCPSecret
		expectedToDelete   []GCPSecret
	}{
		{
			name:               "no diff - no collections, no secrets",
			desiredCollections: map[string]bool{},
			desiredSecrets:     map[string]GCPSecret{},
			actualSecrets:      map[string]GCPSecret{},
			expectedToCreate:   map[string]GCPSecret{},
			expectedToDelete:   []GCPSecret{},
		},
		{
			name: "no diff",
			desiredCollections: map[string]bool{
				"test-collection": true,
			},
			desiredSecrets: map[string]GCPSecret{
				GetUpdaterSASecretName("test-collection"): {
					Name:       GetUpdaterSASecretName("test-collection"),
					Type:       SecretTypeSA,
					Collection: "test-collection",
				},
				GetIndexSecretName("test-collection"): {
					Name:       GetIndexSecretName("test-collection"),
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
			},
			actualSecrets: map[string]GCPSecret{
				GetIndexSecretName("test-collection"): {
					Name:       GetIndexSecretName("test-collection"),
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
				GetUpdaterSASecretName("test-collection"): {
					Name:       GetUpdaterSASecretName("test-collection"),
					Type:       SecretTypeSA,
					Collection: "test-collection",
				},
			},
			expectedToCreate: map[string]GCPSecret{},
			expectedToDelete: []GCPSecret{},
		},
		{
			name: "one new secret to create",
			desiredCollections: map[string]bool{
				"test-collection-new": true,
			},
			desiredSecrets: map[string]GCPSecret{
				GetUpdaterSASecretName("test-collection"): SAsecret,
			},
			actualSecrets: map[string]GCPSecret{},
			expectedToCreate: map[string]GCPSecret{
				GetUpdaterSASecretName("test-collection"): SAsecret,
			},
			expectedToDelete: []GCPSecret{},
		},
		{
			name:               "basic delete",
			desiredCollections: map[string]bool{},
			desiredSecrets:     map[string]GCPSecret{},
			actualSecrets: map[string]GCPSecret{
				GetUpdaterSASecretName(testCollection): SAsecret,
				GetIndexSecretName(testCollection):     indexSecret,
			},
			expectedToCreate: map[string]GCPSecret{},
			expectedToDelete: []GCPSecret{indexSecret, SAsecret},
		},
		{
			name: "selective collection deletion - keep one, delete another",
			desiredCollections: map[string]bool{
				"keep-collection": true,
			},
			desiredSecrets: map[string]GCPSecret{
				GetUpdaterSASecretName("keep-collection"): {
					Name:       GetUpdaterSASecretName("keep-collection"),
					Type:       SecretTypeSA,
					Collection: "keep-collection",
				},
				GetIndexSecretName("keep-collection"): {
					Name:       GetIndexSecretName("keep-collection"),
					Type:       SecretTypeIndex,
					Collection: "keep-collection",
				},
			},
			actualSecrets: map[string]GCPSecret{
				GetUpdaterSASecretName("keep-collection"): {
					Name:       GetUpdaterSASecretName("keep-collection"),
					Type:       SecretTypeSA,
					Collection: "keep-collection",
				},
				GetIndexSecretName("keep-collection"): {
					Name:       GetIndexSecretName("keep-collection"),
					Type:       SecretTypeIndex,
					Collection: "keep-collection",
				},
				GetUpdaterSASecretName("delete-collection"): {
					Name:       GetUpdaterSASecretName("delete-collection"),
					Type:       SecretTypeSA,
					Collection: "delete-collection",
				},
				GetIndexSecretName("delete-collection"): {
					Name:       GetIndexSecretName("delete-collection"),
					Type:       SecretTypeIndex,
					Collection: "delete-collection",
				},
			},
			expectedToCreate: map[string]GCPSecret{},
			expectedToDelete: []GCPSecret{
				{
					Name:       GetIndexSecretName("delete-collection"),
					Type:       SecretTypeIndex,
					Collection: "delete-collection",
				},
				{
					Name:       GetUpdaterSASecretName("delete-collection"),
					Type:       SecretTypeSA,
					Collection: "delete-collection",
				},
			},
		},
		{
			name: "mixed operations - create some, delete others",
			desiredCollections: map[string]bool{
				"new-collection": true,
				"existing-keep":  true,
			},
			desiredSecrets: map[string]GCPSecret{
				GetUpdaterSASecretName("new-collection"): {
					Name:       GetUpdaterSASecretName("new-collection"),
					Type:       SecretTypeSA,
					Collection: "new-collection",
				},
				GetIndexSecretName("new-collection"): {
					Name:       GetIndexSecretName("new-collection"),
					Type:       SecretTypeIndex,
					Collection: "new-collection",
				},
				GetUpdaterSASecretName("existing-keep"): {
					Name:       GetUpdaterSASecretName("existing-keep"),
					Type:       SecretTypeSA,
					Collection: "existing-keep",
				},
				GetIndexSecretName("existing-keep"): {
					Name:       GetIndexSecretName("existing-keep"),
					Type:       SecretTypeIndex,
					Collection: "existing-keep",
				},
			},
			actualSecrets: map[string]GCPSecret{
				GetUpdaterSASecretName("existing-keep"): {
					Name:       GetUpdaterSASecretName("existing-keep"),
					Type:       SecretTypeSA,
					Collection: "existing-keep",
				},
				GetIndexSecretName("existing-keep"): {
					Name:       GetIndexSecretName("existing-keep"),
					Type:       SecretTypeIndex,
					Collection: "existing-keep",
				},
				GetUpdaterSASecretName("existing-delete"): {
					Name:       GetUpdaterSASecretName("existing-delete"),
					Type:       SecretTypeSA,
					Collection: "existing-delete",
				},
				GetIndexSecretName("existing-delete"): {
					Name:       GetIndexSecretName("existing-delete"),
					Type:       SecretTypeIndex,
					Collection: "existing-delete",
				},
			},
			expectedToCreate: map[string]GCPSecret{
				GetUpdaterSASecretName("new-collection"): {
					Name:       GetUpdaterSASecretName("new-collection"),
					Type:       SecretTypeSA,
					Collection: "new-collection",
				},
				GetIndexSecretName("new-collection"): {
					Name:       GetIndexSecretName("new-collection"),
					Type:       SecretTypeIndex,
					Collection: "new-collection",
				},
			},
			expectedToDelete: []GCPSecret{
				{
					Name:       GetIndexSecretName("existing-delete"),
					Type:       SecretTypeIndex,
					Collection: "existing-delete",
				},
				{
					Name:       GetUpdaterSASecretName("existing-delete"),
					Type:       SecretTypeSA,
					Collection: "existing-delete",
				},
			},
		},
		{
			name: "partial secret creation - only SA secret missing",
			desiredCollections: map[string]bool{
				"partial-collection": true,
			},
			desiredSecrets: map[string]GCPSecret{
				GetUpdaterSASecretName("partial-collection"): {
					Name:       GetUpdaterSASecretName("partial-collection"),
					Type:       SecretTypeSA,
					Collection: "partial-collection",
				},
				GetIndexSecretName("partial-collection"): {
					Name:       GetIndexSecretName("partial-collection"),
					Type:       SecretTypeIndex,
					Collection: "partial-collection",
				},
			},
			actualSecrets: map[string]GCPSecret{
				GetIndexSecretName("partial-collection"): {
					Name:       GetIndexSecretName("partial-collection"),
					Type:       SecretTypeIndex,
					Collection: "partial-collection",
				},
			},
			expectedToCreate: map[string]GCPSecret{
				GetUpdaterSASecretName("partial-collection"): {
					Name:       GetUpdaterSASecretName("partial-collection"),
					Type:       SecretTypeSA,
					Collection: "partial-collection",
				},
			},
			expectedToDelete: []GCPSecret{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			toCreate, toDelete := DiffSecrets(tc.desiredSecrets, tc.actualSecrets, tc.desiredCollections)
			testhelper.Diff(t, "secrets to create", toCreate, tc.expectedToCreate)
			testhelper.Diff(t, "secrets to delete", toDelete, tc.expectedToDelete)
		})
	}
}

func TestDiffIAMBindings(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

	// Helper to create test bindings
	createViewerBinding := func(collection string, members []string) *iampb.Binding {
		return &iampb.Binding{
			Role:    config.GetSecretAccessorRole(),
			Members: members,
			Condition: &expr.Expr{
				Expression:  BuildSecretAccessorRoleConditionExpression(collection),
				Title:       GetSecretsViewerConditionTitle(collection),
				Description: GetSecretsViewerConditionDescription(collection),
			},
		}
	}

	createUpdaterBinding := func(collection string, members []string) *iampb.Binding {
		return &iampb.Binding{
			Role:    config.GetSecretUpdaterRole(),
			Members: members,
			Condition: &expr.Expr{
				Expression:  BuildSecretUpdaterRoleConditionExpression(collection),
				Title:       GetSecretsUpdaterConditionTitle(collection),
				Description: GetSecretsUpdaterConditionDescription(collection),
			},
		}
	}

	createUnmanagedBinding := func() *iampb.Binding {
		return &iampb.Binding{
			Role:    "roles/owner",
			Members: []string{"user:admin@example.com"},
		}
	}

	testCases := []struct {
		name             string
		desiredBindings  []*iampb.Binding
		actualPolicy     *iampb.Policy
		expectNoChanges  bool
		expectChanges    bool
		expectedBindings int // expected number of bindings in final policy
	}{
		{
			name:            "no changes - empty desired and actual",
			desiredBindings: []*iampb.Binding{},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{},
				Etag:     []byte("test-etag"),
				Version:  3,
			},
			expectNoChanges: true,
		},
		{
			name: "no changes - identical bindings",
			desiredBindings: []*iampb.Binding{
				createViewerBinding("alpha", []string{"user:test@example.com"}),
			},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{
					createViewerBinding("alpha", []string{"user:test@example.com"}),
				},
				Etag:    []byte("test-etag"),
				Version: 3,
			},
			expectNoChanges: true,
		},
		{
			name: "preserve unmanaged bindings",
			desiredBindings: []*iampb.Binding{
				createViewerBinding("alpha", []string{"user:test@example.com", fmt.Sprintf("serviceAccount:alpha%s@example.com", ServiceAccountIDSuffix)}),
			},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{
					createViewerBinding("alpha", []string{"user:test@example.com", fmt.Sprintf("serviceAccount:alpha%s@example.com", ServiceAccountIDSuffix)}),
					createUnmanagedBinding(),
				},
				Etag:    []byte("test-etag"),
				Version: 3,
			},
			expectNoChanges: true,
		},
		{
			name: "create new binding",
			desiredBindings: []*iampb.Binding{
				createViewerBinding("alpha", []string{"user:test@example.com", fmt.Sprintf("serviceAccount:alpha%s@example.com", ServiceAccountIDSuffix)}),
			},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{},
				Etag:     []byte("test-etag"),
				Version:  3,
			},
			expectChanges:    true,
			expectedBindings: 1,
		},
		{
			name:            "delete obsolete managed binding",
			desiredBindings: []*iampb.Binding{},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{
					createViewerBinding("alpha", []string{"user:test@example.com"}),
				},
				Etag:    []byte("test-etag"),
				Version: 3,
			},
			expectChanges:    true,
			expectedBindings: 0,
		},
		{
			name: "mixed operations - create, keep, delete",
			desiredBindings: []*iampb.Binding{
				createViewerBinding("alpha", []string{"user:test@example.com"}),
				createUpdaterBinding("beta", []string{"user:new@example.com"}),
			},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{
					createViewerBinding("alpha", []string{"user:test@example.com"}),
					createViewerBinding("gamma", []string{"user:old@example.com"}),
					createUnmanagedBinding(),
				},
				Etag:    []byte("test-etag"),
				Version: 3,
			},
			expectChanges:    true,
			expectedBindings: 3,
		},
		{
			name: "complex scenario - multiple collections",
			desiredBindings: []*iampb.Binding{
				createViewerBinding("test", []string{"user:test@example.com", "group:test-team@example.com"}),
				createUpdaterBinding("test", []string{"user:test@example.com", "group:test-team@example.com"}),
				createViewerBinding("admin", []string{"user:admin@example.com"}),
				createUpdaterBinding("admin", []string{"user:admin@example.com"}),
			},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{
					createViewerBinding("test", []string{"user:test@example.com", "group:test-team@example.com"}),
					createUpdaterBinding("test", []string{"user:test@example.com", "group:test-team@example.com"}),
					createViewerBinding("obsolete", []string{"user:obsolete@example.com"}),
					createUpdaterBinding("obsolete", []string{"user:obsolete@example.com"}),
					createUnmanagedBinding(),
				},
				Etag:    []byte("test-etag"),
				Version: 3,
			},
			expectChanges:    true,
			expectedBindings: 5,
		},
		{
			name: "only unmanaged bindings exist",
			desiredBindings: []*iampb.Binding{
				createViewerBinding("alpha", []string{"user:test@example.com"}),
			},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{
					createUnmanagedBinding(),
					{
						Role:    "roles/viewer",
						Members: []string{"user:viewer@example.com"},
					},
				},
				Etag:    []byte("test-etag"),
				Version: 3,
			},
			expectChanges:    true,
			expectedBindings: 3,
		},
		{
			name: "member order difference (should be no change)",
			desiredBindings: []*iampb.Binding{
				createViewerBinding("alpha", []string{"user:a@example.com", "user:b@example.com"}),
			},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{
					createViewerBinding("alpha", []string{"user:b@example.com", "user:a@example.com"}),
				},
				Etag:    []byte("test-etag"),
				Version: 3,
			},
			expectNoChanges: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := DiffIAMBindings(tc.desiredBindings, tc.actualPolicy)

			if tc.expectNoChanges {
				if result != nil {
					t.Errorf("Expected no changes (nil result), but got policy with %d bindings", len(result.Bindings))
				}
				return
			}

			if tc.expectChanges {
				if result == nil {
					t.Errorf("Expected changes but got nil result")
					return
				}

				// Verify policy structure
				if !bytes.Equal(result.Etag, tc.actualPolicy.Etag) {
					t.Errorf("Expected Etag %s, got %s", tc.actualPolicy.Etag, result.Etag)
				}
				if result.Version != 3 {
					t.Errorf("Expected Version 3, got %d", result.Version)
				}

				// Verify binding count
				if len(result.Bindings) != tc.expectedBindings {
					t.Errorf("Expected %d bindings in result, got %d", tc.expectedBindings, len(result.Bindings))
				}

				// Verify all desired bindings are present
				desiredKeys := make(map[string]bool)
				for _, desired := range tc.desiredBindings {
					key := ToCanonicalIAMBinding(desired).makeCanonicalKey()
					desiredKeys[key] = true
				}

				foundDesired := 0
				for _, binding := range result.Bindings {
					key := ToCanonicalIAMBinding(binding).makeCanonicalKey()
					if desiredKeys[key] {
						foundDesired++
					}
				}

				if foundDesired != len(tc.desiredBindings) {
					t.Errorf("Expected %d desired bindings in result, found %d", len(tc.desiredBindings), foundDesired)
				}

				// Verify no obsolete managed bindings remain
				for _, binding := range result.Bindings {
					if IsManagedBinding(binding) {
						key := ToCanonicalIAMBinding(binding).makeCanonicalKey()
						if !desiredKeys[key] {
							t.Errorf("Found obsolete managed binding in result: Role=%s, Condition=%s",
								binding.Role, binding.Condition.GetTitle())
						}
					}
				}
			}
		})
	}
}
