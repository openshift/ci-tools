package main

import (
	"fmt"
	"testing"

	"cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/genproto/googleapis/type/expr"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

// makeServiceAccount is a helper function to create ServiceAccountInfo for testing
func makeServiceAccount(collection string) ServiceAccountInfo {
	return ServiceAccountInfo{
		Email:       getUpdaterSAEmail(collection),
		DisplayName: getUpdaterSAId(collection),
		Collection:  collection,
	}
}

func TestDiffServiceAccounts(t *testing.T) {
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
				Role:      SecretAccessorRole,
				Members:   "user:test@example.com",
				Condition: "",
			},
		},
		{
			name: "binding with condition",
			binding: &iampb.Binding{
				Role:    SecretAccessorRole,
				Members: []string{"user:test@example.com", getUpdaterSAEmail("collection1")},
				Condition: &expr.Expr{
					Expression: fmt.Sprintf(`resource.name.startsWith("projects/%s/secrets/collection1__")`, ProjectId),
					Title:      "collection1_updater_condition",
				},
			},
			expected: CanonicalIAMBinding{
				Role:      SecretAccessorRole,
				Members:   fmt.Sprintf("%s,user:test@example.com", getUpdaterSAEmail("collection1")),
				Condition: fmt.Sprintf(`resource.name.startsWith("projects/%s/secrets/collection1__")`, ProjectId),
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
			name:       "unknown secret is classified as unknown",
			secretName: "collection1__some-random-secret",
			expected:   SecretTypeUnknown,
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
