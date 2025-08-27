package gsmsecrets

import (
	"fmt"
	"testing"

	"cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/genproto/googleapis/type/expr"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestMakeCanonicalKey(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

	testCases := []struct {
		name    string
		binding CanonicalIAMBinding
	}{
		{
			name: "simple binding without condition",
			binding: CanonicalIAMBinding{
				Role:    config.GetSecretAccessorRole(),
				Members: "user:test@example.com",
			},
		},
		{
			name: "binding with full condition",
			binding: CanonicalIAMBinding{
				Role:           config.GetSecretAccessorRole(),
				Members:        "group:team@example.com,user:test@example.com",
				ConditionTitle: GetSecretsViewerConditionTitle("alpha"),
				ConditionDesc:  GetSecretsViewerConditionDescription("alpha"),
				ConditionExpr:  BuildSecretAccessorRoleConditionExpression("alpha"),
			},
		},
		{
			name: "updater binding with condition",
			binding: CanonicalIAMBinding{
				Role:           config.GetSecretUpdaterRole(),
				Members:        fmt.Sprintf("serviceAccount:%s", GetUpdaterSAEmail("beta", config)),
				ConditionTitle: GetSecretsUpdaterConditionTitle("beta"),
				ConditionDesc:  GetSecretsUpdaterConditionDescription("beta"),
				ConditionExpr:  BuildSecretUpdaterRoleConditionExpression("beta"),
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
				Role:           config.GetSecretAccessorRole(),
				Members:        "group:admin@example.com,group:dev@example.com,user:alice@example.com,user:bob@example.com",
				ConditionTitle: GetSecretsViewerConditionTitle("gamma"),
				ConditionDesc:  GetSecretsViewerConditionDescription("gamma"),
				ConditionExpr:  BuildSecretAccessorRoleConditionExpression("gamma"),
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

func TestToCanonicalIAMBinding(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

	testCases := []struct {
		name     string
		binding  *iampb.Binding
		expected CanonicalIAMBinding
	}{
		{
			name: "simple binding",
			binding: &iampb.Binding{
				Role:    config.GetSecretAccessorRole(),
				Members: []string{"user:test@example.com"},
			},
			expected: CanonicalIAMBinding{
				Role:    config.GetSecretAccessorRole(),
				Members: "user:test@example.com",
			},
		},
		{
			name: "binding with condition",
			binding: &iampb.Binding{
				Role:    config.GetSecretAccessorRole(),
				Members: []string{"user:test@example.com", GetUpdaterSAEmail("collection1", config)},
				Condition: &expr.Expr{
					Expression: fmt.Sprintf(`resource.name.startsWith("projects/%s/secrets/collection1__")`, config.ProjectIdString),
					Title:      "some title",
				},
			},
			expected: CanonicalIAMBinding{
				Role:           config.GetSecretAccessorRole(),
				Members:        fmt.Sprintf("%s,user:test@example.com", GetUpdaterSAEmail("collection1", config)),
				ConditionExpr:  fmt.Sprintf(`resource.name.startsWith("projects/%s/secrets/collection1__")`, config.ProjectIdString),
				ConditionTitle: "some title",
			},
		},
		{
			name: "complex binding",
			binding: &iampb.Binding{
				Role: config.GetSecretUpdaterRole(),
				Members: []string{
					"user:test@example.com",
					"user:some-other-user@example.com",
					GetUpdaterSAEmail("collection1", config),
				},
				Condition: &expr.Expr{
					Title:       GetSecretsUpdaterConditionTitle("collection1"),
					Description: GetSecretsUpdaterConditionDescription("collection1"),
					Expression:  BuildSecretUpdaterRoleConditionExpression("collection1"),
				},
			},
			expected: CanonicalIAMBinding{
				Role:           config.GetSecretUpdaterRole(),
				Members:        fmt.Sprintf("%s,user:some-other-user@example.com,user:test@example.com", GetUpdaterSAEmail("collection1", config)),
				ConditionExpr:  BuildSecretUpdaterRoleConditionExpression("collection1"),
				ConditionTitle: GetSecretsUpdaterConditionTitle("collection1"),
				ConditionDesc:  GetSecretsUpdaterConditionDescription("collection1"),
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

func TestIsManagedBinding(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

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
				Role:    config.GetSecretAccessorRole(),
				Members: []string{"user:test@example.com"},
			},
			expected: false,
		},
		{
			name: "correct binding",
			binding: &iampb.Binding{
				Role:    config.GetSecretUpdaterRole(),
				Members: []string{"serviceAccount:test@example.com"},
				Condition: &expr.Expr{
					Title:       GetSecretsUpdaterConditionTitle("test-collection"),
					Description: GetSecretsUpdaterConditionDescription("test-collection"),
					Expression:  BuildSecretUpdaterRoleConditionExpression("test-collection"),
				},
			},
			expected: true,
		},
		{
			name: "correct binding with different title",
			binding: &iampb.Binding{
				Role:    config.GetSecretUpdaterRole(),
				Members: []string{"serviceAccount:test@example.com"},
				Condition: &expr.Expr{
					Title:       "some wrong title",
					Description: GetSecretsUpdaterConditionDescription("test-collection"),
					Expression:  BuildSecretUpdaterRoleConditionExpression("test-collection"),
				},
			},
			expected: false,
		},
		{
			name: "correct binding with different description",
			binding: &iampb.Binding{
				Role:    config.GetSecretUpdaterRole(),
				Members: []string{"serviceAccount:test@example.com"},
				Condition: &expr.Expr{
					Title:       GetSecretsUpdaterConditionTitle("test-collection"),
					Description: "some wrong description",
					Expression:  BuildSecretUpdaterRoleConditionExpression("test-collection"),
				},
			},
			expected: false,
		},
		{
			name: "correct binding with different expression",
			binding: &iampb.Binding{
				Role: config.GetSecretUpdaterRole(),
				Condition: &expr.Expr{
					Title:       GetSecretsUpdaterConditionTitle("test-collection"),
					Description: GetSecretsUpdaterConditionDescription("test-collection"),
					Expression:  "some wrong expression",
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := IsManagedBinding(tc.binding)
			if actual != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, actual)
			}
		})
	}
}
