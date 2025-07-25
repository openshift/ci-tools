package main

import (
	"bytes"
	"fmt"
	"testing"

	"cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/genproto/googleapis/type/expr"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestDiffServiceAccounts(t *testing.T) {
	makeServiceAccount := func(collection string) ServiceAccountInfo {
		return ServiceAccountInfo{
			Email:       getUpdaterSAEmail(collection),
			DisplayName: getUpdaterSAId(collection),
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
				getUpdaterSAEmail("alpha"): serviceAccountAlpha,
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
				getUpdaterSAEmail("alpha"): serviceAccountAlpha,
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
				getUpdaterSAEmail("gamma"): serviceAccountGamma,
			},
			expectedToDelete: SAMap{
				getUpdaterSAEmail("beta"): serviceAccountBeta,
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
				getUpdaterSAEmail("prod"):    serviceAccountProd,
				getUpdaterSAEmail("staging"): serviceAccountStaging,
				getUpdaterSAEmail("dev"):     serviceAccountDev,
			},
			expectedToDelete: SAMap{
				getUpdaterSAEmail("beta"):   serviceAccountBeta,
				getUpdaterSAEmail("legacy"): serviceAccountLegacy,
				getUpdaterSAEmail("temp"):   serviceAccountTemp,
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
				getUpdaterSAEmail("delta"):   serviceAccountDelta,
				getUpdaterSAEmail("epsilon"): serviceAccountEpsilon,
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
				getUpdaterSAEmail("alpha"):  serviceAccountAlpha,
				getUpdaterSAEmail("beta"):   serviceAccountBeta,
				getUpdaterSAEmail("legacy"): serviceAccountLegacy,
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
				getUpdaterSAEmail("alpha"): serviceAccountAlpha,
				getUpdaterSAEmail("beta"):  serviceAccountBeta,
				getUpdaterSAEmail("zeta"):  serviceAccountZeta,
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
		Name:       getUpdaterSASecretName(testCollection),
		Type:       SecretTypeSA,
		Collection: testCollection,
	}
	indexSecret := GCPSecret{
		Name:       getIndexSecretName(testCollection),
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
				getUpdaterSASecretName("test-collection"): {
					Name:       getUpdaterSASecretName("test-collection"),
					Type:       SecretTypeSA,
					Collection: "test-collection",
				},
				getIndexSecretName("test-collection"): {
					Name:       getIndexSecretName("test-collection"),
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
			},
			actualSecrets: map[string]GCPSecret{
				getIndexSecretName("test-collection"): {
					Name:       getIndexSecretName("test-collection"),
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
				getUpdaterSASecretName("test-collection"): {
					Name:       getUpdaterSASecretName("test-collection"),
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
				getUpdaterSASecretName("test-collection"): SAsecret,
			},
			actualSecrets: map[string]GCPSecret{},
			expectedToCreate: map[string]GCPSecret{
				getUpdaterSASecretName("test-collection"): SAsecret,
			},
			expectedToDelete: []GCPSecret{},
		},
		{
			name:               "basic delete",
			desiredCollections: map[string]bool{},
			desiredSecrets:     map[string]GCPSecret{},
			actualSecrets: map[string]GCPSecret{
				getUpdaterSASecretName(testCollection): SAsecret,
				getIndexSecretName(testCollection):     indexSecret,
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
				getUpdaterSASecretName("keep-collection"): {
					Name:       getUpdaterSASecretName("keep-collection"),
					Type:       SecretTypeSA,
					Collection: "keep-collection",
				},
				getIndexSecretName("keep-collection"): {
					Name:       getIndexSecretName("keep-collection"),
					Type:       SecretTypeIndex,
					Collection: "keep-collection",
				},
			},
			actualSecrets: map[string]GCPSecret{
				getUpdaterSASecretName("keep-collection"): {
					Name:       getUpdaterSASecretName("keep-collection"),
					Type:       SecretTypeSA,
					Collection: "keep-collection",
				},
				getIndexSecretName("keep-collection"): {
					Name:       getIndexSecretName("keep-collection"),
					Type:       SecretTypeIndex,
					Collection: "keep-collection",
				},
				getUpdaterSASecretName("delete-collection"): {
					Name:       getUpdaterSASecretName("delete-collection"),
					Type:       SecretTypeSA,
					Collection: "delete-collection",
				},
				getIndexSecretName("delete-collection"): {
					Name:       getIndexSecretName("delete-collection"),
					Type:       SecretTypeIndex,
					Collection: "delete-collection",
				},
			},
			expectedToCreate: map[string]GCPSecret{},
			expectedToDelete: []GCPSecret{
				{
					Name:       getIndexSecretName("delete-collection"),
					Type:       SecretTypeIndex,
					Collection: "delete-collection",
				},
				{
					Name:       getUpdaterSASecretName("delete-collection"),
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
				getUpdaterSASecretName("new-collection"): {
					Name:       getUpdaterSASecretName("new-collection"),
					Type:       SecretTypeSA,
					Collection: "new-collection",
				},
				getIndexSecretName("new-collection"): {
					Name:       getIndexSecretName("new-collection"),
					Type:       SecretTypeIndex,
					Collection: "new-collection",
				},
				getUpdaterSASecretName("existing-keep"): {
					Name:       getUpdaterSASecretName("existing-keep"),
					Type:       SecretTypeSA,
					Collection: "existing-keep",
				},
				getIndexSecretName("existing-keep"): {
					Name:       getIndexSecretName("existing-keep"),
					Type:       SecretTypeIndex,
					Collection: "existing-keep",
				},
			},
			actualSecrets: map[string]GCPSecret{
				getUpdaterSASecretName("existing-keep"): {
					Name:       getUpdaterSASecretName("existing-keep"),
					Type:       SecretTypeSA,
					Collection: "existing-keep",
				},
				getIndexSecretName("existing-keep"): {
					Name:       getIndexSecretName("existing-keep"),
					Type:       SecretTypeIndex,
					Collection: "existing-keep",
				},
				getUpdaterSASecretName("existing-delete"): {
					Name:       getUpdaterSASecretName("existing-delete"),
					Type:       SecretTypeSA,
					Collection: "existing-delete",
				},
				getIndexSecretName("existing-delete"): {
					Name:       getIndexSecretName("existing-delete"),
					Type:       SecretTypeIndex,
					Collection: "existing-delete",
				},
			},
			expectedToCreate: map[string]GCPSecret{
				getUpdaterSASecretName("new-collection"): {
					Name:       getUpdaterSASecretName("new-collection"),
					Type:       SecretTypeSA,
					Collection: "new-collection",
				},
				getIndexSecretName("new-collection"): {
					Name:       getIndexSecretName("new-collection"),
					Type:       SecretTypeIndex,
					Collection: "new-collection",
				},
			},
			expectedToDelete: []GCPSecret{
				{
					Name:       getIndexSecretName("existing-delete"),
					Type:       SecretTypeIndex,
					Collection: "existing-delete",
				},
				{
					Name:       getUpdaterSASecretName("existing-delete"),
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
				getUpdaterSASecretName("partial-collection"): {
					Name:       getUpdaterSASecretName("partial-collection"),
					Type:       SecretTypeSA,
					Collection: "partial-collection",
				},
				getIndexSecretName("partial-collection"): {
					Name:       getIndexSecretName("partial-collection"),
					Type:       SecretTypeIndex,
					Collection: "partial-collection",
				},
			},
			actualSecrets: map[string]GCPSecret{
				getIndexSecretName("partial-collection"): {
					Name:       getIndexSecretName("partial-collection"),
					Type:       SecretTypeIndex,
					Collection: "partial-collection",
				},
			},
			expectedToCreate: map[string]GCPSecret{
				getUpdaterSASecretName("partial-collection"): {
					Name:       getUpdaterSASecretName("partial-collection"),
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
	// Helper to create test bindings
	createViewerBinding := func(collection string, members []string) *iampb.Binding {
		return &iampb.Binding{
			Role:    SecretAccessorRole,
			Members: members,
			Condition: &expr.Expr{
				Expression:  buildSecretAccessorRoleConditionExpression(collection),
				Title:       getSecretsViewerConditionTitle(collection),
				Description: getSecretsViewerConditionDescription(collection),
			},
		}
	}

	createUpdaterBinding := func(collection string, members []string) *iampb.Binding {
		return &iampb.Binding{
			Role:    SecretUpdaterRole,
			Members: members,
			Condition: &expr.Expr{
				Expression:  buildSecretUpdaterRoleConditionExpression(collection),
				Title:       getSecretsUpdaterConditionTitle(collection),
				Description: getSecretsUpdaterConditionDescription(collection),
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
				createViewerBinding("alpha", []string{"user:test@example.com", "serviceAccount:alpha-updater@example.com"}),
			},
			actualPolicy: &iampb.Policy{
				Bindings: []*iampb.Binding{
					createViewerBinding("alpha", []string{"user:test@example.com", "serviceAccount:alpha-updater@example.com"}),
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
				createViewerBinding("alpha", []string{"user:test@example.com", "serviceAccount:alpha-updater@example.com"}),
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
					if isManagedBinding(binding) {
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

func TestClassifySecret(t *testing.T) {
	testCases := []struct {
		name       string
		secretName string
		expected   SecretType
	}{
		{
			name:       "updater-sa secret is classified as SA",
			secretName: "collection1__updater-service-account",
			expected:   SecretTypeSA,
		},
		{
			name:       "index secret is classified as index",
			secretName: "collection1____index",
			expected:   SecretTypeIndex,
		},
		{
			name:       "secret is classified a common secret",
			secretName: "collection1__some-random-secret",
			expected:   SecretTypeGeneric,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := classifySecret(tc.secretName)
			if actual != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, actual)
			}
		})
	}
}

func TestIsManagedBinding(t *testing.T) {
	testCases := []struct {
		name     string
		binding  *iampb.Binding
		expected bool
	}{
		{
			name: "another role",
			binding: &iampb.Binding{
				Role:    "roles/owner",
				Members: []string{"user:test@example.com"},
			},
			expected: false,
		},
		{
			name: "binding with no condition",
			binding: &iampb.Binding{
				Role:    SecretAccessorRole,
				Members: []string{"user:test@example.com"},
			},
			expected: false,
		},
		{
			name: "correct binding",
			binding: &iampb.Binding{
				Role:    SecretUpdaterRole,
				Members: []string{"serviceAccount:test@example.com"},
				Condition: &expr.Expr{
					Title:       getSecretsUpdaterConditionTitle("test-collection"),
					Description: getSecretsUpdaterConditionDescription("test-collection"),
					Expression:  buildSecretUpdaterRoleConditionExpression("test-collection"),
				},
			},
			expected: true,
		},
		{
			name: "correct binding with different title",
			binding: &iampb.Binding{
				Role:    SecretUpdaterRole,
				Members: []string{"serviceAccount:test@example.com"},
				Condition: &expr.Expr{
					Title:       "some wrong title",
					Description: getSecretsUpdaterConditionDescription("test-collection"),
					Expression:  buildSecretUpdaterRoleConditionExpression("test-collection"),
				},
			},
			expected: false,
		},
		{
			name: "correct binding with different description",
			binding: &iampb.Binding{
				Role:    SecretUpdaterRole,
				Members: []string{"serviceAccount:test@example.com"},
				Condition: &expr.Expr{
					Title:       getSecretsUpdaterConditionTitle("test-collection"),
					Description: "some wrong description",
					Expression:  buildSecretUpdaterRoleConditionExpression("test-collection"),
				},
			},
			expected: false,
		},
		{
			name: "correct binding with different expression",
			binding: &iampb.Binding{
				Role: SecretUpdaterRole,
				Condition: &expr.Expr{
					Title:       getSecretsUpdaterConditionTitle("test-collection"),
					Description: getSecretsUpdaterConditionDescription("test-collection"),
					Expression:  "some wrong expression",
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := isManagedBinding(tc.binding)
			if actual != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, actual)
			}
		})
	}
}

func TestToCanonicalIAMBinding(t *testing.T) {
	testCases := []struct {
		name     string
		binding  *iampb.Binding
		expected CanonicalIAMBinding
	}{
		{
			name: "simple binding",
			binding: &iampb.Binding{
				Role:    SecretAccessorRole,
				Members: []string{"user:test@example.com"},
			},
			expected: CanonicalIAMBinding{
				Role:    SecretAccessorRole,
				Members: "user:test@example.com",
			},
		},
		{
			name: "binding with condition",
			binding: &iampb.Binding{
				Role:    SecretAccessorRole,
				Members: []string{"user:test@example.com", getUpdaterSAEmail("collection1")},
				Condition: &expr.Expr{
					Expression: fmt.Sprintf(`resource.name.startsWith("projects/%s/secrets/collection1__")`, ProjectId),
					Title:      "some title",
				},
			},
			expected: CanonicalIAMBinding{
				Role:           SecretAccessorRole,
				Members:        fmt.Sprintf("%s,user:test@example.com", getUpdaterSAEmail("collection1")),
				ConditionExpr:  fmt.Sprintf(`resource.name.startsWith("projects/%s/secrets/collection1__")`, ProjectId),
				ConditionTitle: "some title",
			},
		},
		{
			name: "complex binding",
			binding: &iampb.Binding{
				Role: SecretUpdaterRole,
				Members: []string{
					"user:test@example.com",
					"user:some-other-user@example.com",
					getUpdaterSAEmail("collection1"),
				},
				Condition: &expr.Expr{
					Title:       getSecretsUpdaterConditionTitle("collection1"),
					Description: getSecretsUpdaterConditionDescription("collection1"),
					Expression:  buildSecretUpdaterRoleConditionExpression("collection1"),
				},
			},
			expected: CanonicalIAMBinding{
				Role:           SecretUpdaterRole,
				Members:        fmt.Sprintf("%s,user:some-other-user@example.com,user:test@example.com", getUpdaterSAEmail("collection1")),
				ConditionExpr:  buildSecretUpdaterRoleConditionExpression("collection1"),
				ConditionTitle: getSecretsUpdaterConditionTitle("collection1"),
				ConditionDesc:  getSecretsUpdaterConditionDescription("collection1"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ToCanonicalIAMBinding(tc.binding)
			testhelper.Diff(t, "actual", actual, tc.expected)
		})
	}
}

func TestMakeCanonicalKey(t *testing.T) {
	testCases := []struct {
		name    string
		binding CanonicalIAMBinding
	}{
		{
			name: "simple binding without condition",
			binding: CanonicalIAMBinding{
				Role:    SecretAccessorRole,
				Members: "user:test@example.com",
			},
		},
		{
			name: "binding with full condition",
			binding: CanonicalIAMBinding{
				Role:           SecretAccessorRole,
				Members:        "group:team@example.com,user:test@example.com",
				ConditionTitle: getSecretsViewerConditionTitle("alpha"),
				ConditionDesc:  getSecretsViewerConditionDescription("alpha"),
				ConditionExpr:  buildSecretAccessorRoleConditionExpression("alpha"),
			},
		},
		{
			name: "updater binding with condition",
			binding: CanonicalIAMBinding{
				Role:           SecretUpdaterRole,
				Members:        fmt.Sprintf("serviceAccount:%s", getUpdaterSAEmail("beta")),
				ConditionTitle: getSecretsUpdaterConditionTitle("beta"),
				ConditionDesc:  getSecretsUpdaterConditionDescription("beta"),
				ConditionExpr:  buildSecretUpdaterRoleConditionExpression("beta"),
			},
		},
		{
			name: "binding with empty fields",
			binding: CanonicalIAMBinding{
				Role:           "",
				Members:        "",
				ConditionTitle: "",
				ConditionDesc:  "",
				ConditionExpr:  "",
			},
		},
		{
			name: "multiple members sorted",
			binding: CanonicalIAMBinding{
				Role:           SecretAccessorRole,
				Members:        "group:admin@example.com,group:dev@example.com,user:alice@example.com,user:bob@example.com",
				ConditionTitle: getSecretsViewerConditionTitle("gamma"),
				ConditionDesc:  getSecretsViewerConditionDescription("gamma"),
				ConditionExpr:  buildSecretAccessorRoleConditionExpression("gamma"),
			},
		},
	}

	generatedKeys := make(map[string]string) // Track all generated keys to ensure uniqueness

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			key := tc.binding.makeCanonicalKey()

			if key == "" {
				t.Errorf("makeCanonicalKey() returned empty string")
			}

			if len(key) != 64 {
				t.Errorf("makeCanonicalKey() returned key of length %d, expected 64", len(key))
			}

			key2 := tc.binding.makeCanonicalKey()
			if key != key2 {
				t.Errorf("makeCanonicalKey() is not deterministic: first=%s, second=%s", key, key2)
			}

			if existingTest, exists := generatedKeys[key]; exists {
				t.Errorf("makeCanonicalKey() generated duplicate key %s for test '%s' and '%s'", key, tc.name, existingTest)
			}
			generatedKeys[key] = tc.name
		})
	}
}
