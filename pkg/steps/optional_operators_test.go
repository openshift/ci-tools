package steps

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

type fakeParams map[string]string

func (f fakeParams) Get(name string) (string, error) {
	return f[name], nil
}

func TestResolveOptionalOperator(t *testing.T) {
	testCases := []struct {
		description string
		params      fakeParams

		expectedOO  *optionalOperator
		expectError bool
	}{
		{
			description: "no parameters yield nil",
			params:      fakeParams{},
		},
		{
			description: "only required parameters",
			params: fakeParams{
				"OO_INDEX":   "le index",
				"OO_PACKAGE": "le package",
				"OO_CHANNEL": "le channel",
				"OO_BUNDLE":  "le bundle",
			},
			expectedOO: &optionalOperator{
				Index:   "le index",
				Package: "le package",
				Channel: "le channel",
				Bundle:  "le bundle",
			},
		},
		{
			description: "all parameters",
			params: fakeParams{
				"OO_INDEX":             "le index",
				"OO_PACKAGE":           "le package",
				"OO_CHANNEL":           "le channel",
				"OO_BUNDLE":            "le bundle",
				"OO_INSTALL_NAMESPACE": "le namespace",
				"OO_TARGET_NAMESPACES": "un,deux,trois",
			},
			expectedOO: &optionalOperator{
				Index:            "le index",
				Package:          "le package",
				Channel:          "le channel",
				Bundle:           "le bundle",
				Namespace:        "le namespace",
				TargetNamespaces: []string{"un", "deux", "trois"},
			},
		},
		{
			description: "missing index -> error",
			params: fakeParams{
				"OO_PACKAGE":           "le package",
				"OO_CHANNEL":           "le channel",
				"OO_BUNDLE":            "le bundle",
				"OO_INSTALL_NAMESPACE": "le namespace",
				"OO_TARGET_NAMESPACES": "un,deux,trois",
			},
			expectError: true,
		},
		{
			description: "missing package -> error",
			params: fakeParams{
				"OO_INDEX":             "le index",
				"OO_CHANNEL":           "le channel",
				"OO_BUNDLE":            "le bundle",
				"OO_INSTALL_NAMESPACE": "le namespace",
				"OO_TARGET_NAMESPACES": "un,deux,trois",
			},
			expectError: true,
		},
		{
			description: "missing channel -> error",
			params: fakeParams{
				"OO_INDEX":             "le index",
				"OO_PACKAGE":           "le package",
				"OO_BUNDLE":            "le bundle",
				"OO_INSTALL_NAMESPACE": "le namespace",
				"OO_TARGET_NAMESPACES": "un,deux,trois",
			},
			expectError: true,
		},
		{
			description: "missing bundle -> error",
			params: fakeParams{
				"OO_INDEX":             "le index",
				"OO_PACKAGE":           "le package",
				"OO_INSTALL_NAMESPACE": "le namespace",
				"OO_TARGET_NAMESPACES": "un,deux,trois",
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual, err := resolveOptionalOperator(tc.params)
			if err != nil && !tc.expectError {
				t.Errorf("%s: unexpected error: %v", tc.description, err)
			} else if err == nil && tc.expectError {
				t.Errorf("%s: expected error, got nil", tc.description)
			} else if !tc.expectError && !equality.Semantic.DeepEqual(actual, tc.expectedOO) {
				t.Errorf("%s: result differs from expected:\n%s", tc.description, cmp.Diff(tc.expectedOO, actual))
			}
		})
	}
}

func TestAsEnv(t *testing.T) {
	testCases := []struct {
		description string
		oo          optionalOperator
		expected    []coreapi.EnvVar
	}{
		{
			description: "only the required parameters",
			oo: optionalOperator{
				Index:   "INDEX",
				Package: "PACKAGE",
				Channel: "CHANNEL",
				Bundle:  "BUNDLE",
			},
			expected: []coreapi.EnvVar{
				{Name: "OO_INDEX", Value: "INDEX"},
				{Name: "OO_PACKAGE", Value: "PACKAGE"},
				{Name: "OO_CHANNEL", Value: "CHANNEL"},
				{Name: "OO_BUNDLE", Value: "BUNDLE"},
			},
		},
		{
			description: "with install Namespace",
			oo: optionalOperator{
				Index:     "INDEX",
				Package:   "PACKAGE",
				Channel:   "CHANNEL",
				Bundle:    "BUNDLE",
				Namespace: "NAMESPACE",
			},
			expected: []coreapi.EnvVar{
				{Name: "OO_INDEX", Value: "INDEX"},
				{Name: "OO_PACKAGE", Value: "PACKAGE"},
				{Name: "OO_CHANNEL", Value: "CHANNEL"},
				{Name: "OO_BUNDLE", Value: "BUNDLE"},
				{Name: "OO_INSTALL_NAMESPACE", Value: "NAMESPACE"},
			},
		},
		{
			description: "with target namespaces",
			oo: optionalOperator{
				Index:            "INDEX",
				Package:          "PACKAGE",
				Channel:          "CHANNEL",
				Bundle:           "BUNDLE",
				TargetNamespaces: []string{"NS1", "NS2"},
			},
			expected: []coreapi.EnvVar{
				{Name: "OO_INDEX", Value: "INDEX"},
				{Name: "OO_PACKAGE", Value: "PACKAGE"},
				{Name: "OO_CHANNEL", Value: "CHANNEL"},
				{Name: "OO_BUNDLE", Value: "BUNDLE"},
				{Name: "OO_TARGET_NAMESPACES", Value: "NS1,NS2"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual := tc.oo.asEnv()
			if !equality.Semantic.DeepEqual(actual, tc.expected) {
				t.Errorf("%s: result differs:\n%s", tc.description, cmp.Diff(tc.expected, tc.oo.asEnv()))
			}
		})
	}
}
