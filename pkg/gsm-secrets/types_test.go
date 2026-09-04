package gsmsecrets

import (
	"fmt"
	"strings"
	"testing"
)

func TestGetUpdaterSAEmail(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

	testCases := []struct {
		name          string
		collection    string
		expectedEmail string
	}{
		{
			name:          "standard collection",
			collection:    "alpha-secrets",
			expectedEmail: fmt.Sprintf("%s@test-project.iam.gserviceaccount.com", GetUpdaterSAId("alpha-secrets")),
		},
		{
			name:          "single word collection",
			collection:    "beta",
			expectedEmail: fmt.Sprintf("%s@test-project.iam.gserviceaccount.com", GetUpdaterSAId("beta")),
		},
		{
			name:          "collection with numbers",
			collection:    "test-collection-123",
			expectedEmail: fmt.Sprintf("%s@test-project.iam.gserviceaccount.com", GetUpdaterSAId("test-collection-123")),
		},
		{
			name:          "one letter collection name",
			collection:    "a",
			expectedEmail: fmt.Sprintf("%s@test-project.iam.gserviceaccount.com", GetUpdaterSAId("a")),
		},
		{
			name:          "collection with multiple hyphens",
			collection:    "my-test-collection-name",
			expectedEmail: fmt.Sprintf("%s@test-project.iam.gserviceaccount.com", GetUpdaterSAId("my-test-collection-name")),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetUpdaterSAEmail(tc.collection, config)
			if actual != tc.expectedEmail {
				t.Errorf("Expected %q, got %q", tc.expectedEmail, actual)
			}
		})
	}
}

func TestGetUpdaterSAId(t *testing.T) {
	// path describes which branch of GetUpdaterSAId a collection is expected to hit.
	type path string
	const (
		direct   path = "direct"   // raw ID is the collection name plus suffix and is valid
		hash     path = "hash"     // raw ID is the base64 hash and is valid
		fallback path = "fallback" // raw ID is invalid, so the hex fallback is used
	)

	testCases := []struct {
		name           string
		collection     string
		expectedLength int
		path           path
	}{
		{
			name:           "short collection - direct use",
			collection:     "alpha",
			expectedLength: 13, // "alpha-updater" = 13 chars
			path:           direct,
		},
		{
			name:           "medium collection - direct use",
			collection:     "test-collection",
			expectedLength: 23, // "test-collection-updater" = 23 chars
			path:           direct,
		},
		{
			name:           "collection at limit - direct use",
			collection:     "collection-at-limit-22",
			expectedLength: 30, // "collection-at-limit-22-updater" = 30 chars
			path:           direct,
		},
		{
			name:           "very long collection - valid hash use",
			collection:     "this-is-a-very-long-collection-name-that-exceeds-normal-limits",
			expectedLength: 30,
			path:           hash,
		},
		{
			// Leading digit makes the direct ID invalid (GCP IDs must start with a letter).
			name:           "collection with leading digit - fallback",
			collection:     "3scale-test-image",
			expectedLength: 30,
			path:           fallback,
		},
		{
			// Long collection whose base64url hash contains '_' (invalid in a GCP ID).
			name:           "long collection with underscore in hash - fallback",
			collection:     "cluster-secrets-osl-gcp",
			expectedLength: 30,
			path:           fallback,
		},
		{
			// Long collection whose lowercased hash starts with a digit (invalid).
			name:           "long collection with leading digit in hash - fallback",
			collection:     "vsphere-assisted-installer-ci",
			expectedLength: 30,
			path:           fallback,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetUpdaterSAId(tc.collection)

			if len(actual) != tc.expectedLength {
				t.Errorf("Expected length %d, got %d for ID %q", tc.expectedLength, len(actual), actual)
			}
			if !strings.HasSuffix(actual, ServiceAccountIDSuffix) {
				t.Errorf("Expected ID %q to end with %q", actual, ServiceAccountIDSuffix)
			}
			// Every returned ID must satisfy GCP's rules and be deterministic.
			if !isValidGCPServiceAccountID(actual) {
				t.Errorf("ID %q is not a valid GCP service account ID", actual)
			}
			if again := GetUpdaterSAId(tc.collection); again != actual {
				t.Errorf("GetUpdaterSAId is not deterministic: got %q then %q", actual, again)
			}

			directId := fmt.Sprintf("%s%s", tc.collection, ServiceAccountIDSuffix)
			switch tc.path {
			case direct:
				if actual != directId {
					t.Errorf("Expected direct ID %q, but got %q", directId, actual)
				}
			case hash:
				if actual == directId {
					t.Errorf("Expected hash to be used for long collection %q, but got direct ID", tc.collection)
				}
			case fallback:
				if raw := rawUpdaterSAId(tc.collection); isValidGCPServiceAccountID(raw) {
					t.Errorf("Expected raw ID %q to be invalid so the fallback is exercised", raw)
				}
				if want := fallbackUpdaterSAId(tc.collection); actual != want {
					t.Errorf("Expected fallback ID %q, but got %q", want, actual)
				}
			}
		})
	}
}

func TestIsValidGCPServiceAccountID(t *testing.T) {
	testCases := []struct {
		name  string
		id    string
		valid bool
	}{
		{name: "valid direct id", id: "alpha-updater", valid: true},
		{name: "valid hex fallback id", id: "s64f7ba388325bd0dafee3-updater", valid: true},
		{name: "valid with double hyphen", id: "vfxj0ci--zubgj8gdeo4ec-updater", valid: true},
		{name: "leading digit", id: "3scale-test-image-updater", valid: false},
		{name: "contains underscore", id: "vbapy_ashggzdty6ydupia-updater", valid: false},
		{name: "leading hyphen", id: "-alpha-updater", valid: false},
		{name: "uppercase", id: "Alpha-updater", valid: false},
		{name: "too short", id: "a-b", valid: false},
		{name: "too long", id: strings.Repeat("a", GCPMaxServiceAccountIDLength+1), valid: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidGCPServiceAccountID(tc.id); got != tc.valid {
				t.Errorf("isValidGCPServiceAccountID(%q) = %v, want %v", tc.id, got, tc.valid)
			}
		})
	}
}

func TestGetIndexSecretName(t *testing.T) {
	testCases := []struct {
		name               string
		collection         string
		expectedSecretName string
	}{
		{
			name:               "standard collection",
			collection:         "alpha-secrets",
			expectedSecretName: "alpha-secrets____index",
		},
		{
			name:               "single word collection",
			collection:         "beta",
			expectedSecretName: "beta____index",
		},
		{
			name:               "collection with numbers",
			collection:         "test-collection-123",
			expectedSecretName: "test-collection-123____index",
		},
		{
			name:               "short collection name",
			collection:         "a",
			expectedSecretName: "a____index",
		},
		{
			name:               "empty collection",
			collection:         "",
			expectedSecretName: "____index",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetIndexSecretName(tc.collection)
			if actual != tc.expectedSecretName {
				t.Errorf("Expected %q, got %q", tc.expectedSecretName, actual)
			}
		})
	}
}

func TestGetSecretID(t *testing.T) {
	testCases := []struct {
		name       string
		secretName string
		expectedID string
	}{
		{
			name:       "full GCP secret resource name",
			secretName: "projects/openshift-ci-secrets/secrets/alpha-secrets__updater-service-account",
			expectedID: "alpha-secrets__updater-service-account",
		},
		{
			name:       "index secret resource name",
			secretName: "projects/test-project/secrets/beta____index",
			expectedID: "beta____index",
		},
		{
			name:       "generic secret resource name",
			secretName: "projects/my-project/secrets/collection__my-secret",
			expectedID: "collection__my-secret",
		},
		{
			name:       "production secret example",
			secretName: "projects/123456789012/secrets/shared-collection__updater-service-account",
			expectedID: "shared-collection__updater-service-account",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetSecretID(tc.secretName)
			if actual != tc.expectedID {
				t.Errorf("Expected %q, got %q", tc.expectedID, actual)
			}
		})
	}
}

func TestGetUpdaterSADisplayName(t *testing.T) {
	testCases := []struct {
		name       string
		collection string
		expected   string
	}{
		{
			name:       "standard collection",
			collection: "alpha-secrets",
			expected:   "alpha-secrets",
		},
		{
			name:       "long collection name",
			collection: "this-is-a-very-long-collection-name-that-exceeds-normal-limits",
			expected:   "this-is-a-very-long-collection-name-that-exceeds-normal-limits",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetUpdaterSADisplayName(tc.collection)
			if actual != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, actual)
			}
		})
	}
}

func TestGetUpdaterSADescription(t *testing.T) {
	testCases := []struct {
		name       string
		collection string
		expected   string
	}{
		{
			name:       "standard collection",
			collection: "alpha-secrets",
			expected:   fmt.Sprintf("%s%s", ServiceAccountDescriptionPrefix, "alpha-secrets"),
		},
		{
			name:       "collection with numbers",
			collection: "test-collection-123",
			expected:   fmt.Sprintf("%s%s", ServiceAccountDescriptionPrefix, "test-collection-123"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetUpdaterSADescription(tc.collection)
			if actual != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, actual)
			}
		})
	}
}

func TestExtractCollectionFromDescription(t *testing.T) {
	testCases := []struct {
		name        string
		description string
		expected    string
	}{
		{
			name:        "valid description",
			description: "Updater service account for secret collection: alpha-secrets",
			expected:    "alpha-secrets",
		},
		{
			name:        "valid description with long name",
			description: "Updater service account for secret collection: this-is-a-very-long-collection-name",
			expected:    "this-is-a-very-long-collection-name",
		},
		{
			name:        "invalid description",
			description: "Some other description",
			expected:    "",
		},
		{
			name:        "empty description",
			description: "",
			expected:    "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ExtractCollectionFromDescription(tc.description)
			if actual != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, actual)
			}
		})
	}
}

func TestGetGSMSecretName(t *testing.T) {
	testCases := []struct {
		name       string
		collection string
		group      string
		field      string
		expected   string
	}{
		{
			name:       "simple secret with no group hierarchy",
			collection: "my-creds",
			group:      "default",
			field:      "username",
			expected:   "my-creds__default__username",
		},
		{
			name:       "secret with single-level group",
			collection: "vsphere",
			group:      "ibmcloud",
			field:      "password",
			expected:   "vsphere__ibmcloud__password",
		},
		{
			name:       "secret with hierarchical group (single slash)",
			collection: "vsphere",
			group:      "ibmcloud/ci",
			field:      "username",
			expected:   "vsphere__ibmcloud__ci__username",
		},
		{
			name:       "secret with deep hierarchical group (multiple slashes)",
			collection: "telcov10n-ci-network",
			group:      "clusters/hlxcl8/ansible_group_masters",
			field:      "ssh-privatekey",
			expected:   "telcov10n-ci-network__clusters__hlxcl8__ansible_group_masters__ssh-privatekey",
		},
		{
			name:       "secret with empty group",
			collection: "simple",
			group:      "",
			field:      "token",
			expected:   "simple__token",
		},
		{
			name:       "secret with hyphens in field name",
			collection: "dptp",
			group:      "build-farm",
			field:      "sa-ci-operator-build01-config",
			expected:   "dptp__build-farm__sa-ci-operator-build01-config",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetGSMSecretName(tc.collection, tc.group, tc.field)
			if actual != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, actual)
			}
		})
	}
}

func TestGetGSMSecretResourceName(t *testing.T) {
	projectNumber := "384486694155"

	testCases := []struct {
		name       string
		collection string
		group      string
		field      string
		expected   string
	}{
		{
			name:       "simple secret resource name",
			collection: "my-creds",
			group:      "default",
			field:      "username",
			expected:   "projects/384486694155/secrets/my-creds__default__username",
		},
		{
			name:       "hierarchical group resource name",
			collection: "vsphere",
			group:      "ibmcloud/ci",
			field:      "api-key",
			expected:   "projects/384486694155/secrets/vsphere__ibmcloud__ci__api-key",
		},
		{
			name:       "deep hierarchy resource name",
			collection: "telcov10n-ci-network",
			group:      "clusters/hlxcl8/ansible_group_masters",
			field:      "password",
			expected:   "projects/384486694155/secrets/telcov10n-ci-network__clusters__hlxcl8__ansible_group_masters__password",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetGSMSecretResourceName(projectNumber, tc.collection, tc.group, tc.field)
			if actual != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, actual)
			}
		})
	}
}
