package testhelper

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
)

func TestSanitizeString(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		expected string
	}{
		{
			name:     "simple, no changes",
			in:       "my_golden.yaml",
			expected: "zz_fixture_my_golden.yaml",
		},
		{
			name:     "complex",
			in:       "my_Go\\l'de`n.yaml",
			expected: "zz_fixture_my_Go_l_de_n.yaml",
		},
		{
			name:     "no double underscores",
			in:       "a_|",
			expected: "zz_fixture_a_",
		},
		{
			name:     "numbers are kept",
			in:       "0123456789.yaml",
			expected: "zz_fixture_0123456789.yaml",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if result := sanitizeFilename(tc.in); result != tc.expected {
				t.Errorf("expected '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestEquateErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		x        error
		y        error
		expected bool
	}{
		{
			name: "x is nil",
			y:    fmt.Errorf("error y"),
		},
		{
			name: "y is nil",
			x:    fmt.Errorf("error x"),
		},
		{
			name:     "both are nil",
			expected: true,
		},
		{
			name:     "neither are nil",
			x:        fmt.Errorf("same error"),
			y:        fmt.Errorf("same error"),
			expected: true,
		},
		{
			name:     "wrapped errors of fmt type are the same",
			x:        fmt.Errorf("wrap error: same error"),
			y:        fmt.Errorf("wrap error: %w", fmt.Errorf("same error")),
			expected: true,
		},
		{
			name:     "wrapped errors of different type are the same",
			x:        errors.New("wrap error: same error"),
			y:        fmt.Errorf("wrap error: %w", fmt.Errorf("same error")),
			expected: true,
		},
		{
			name:     "wrap error: true",
			x:        fmt.Errorf("wrap error: %w", fmt.Errorf("same error")),
			y:        fmt.Errorf("wrap error: %w", fmt.Errorf("same error")),
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actualDiffString := cmp.Diff(tc.x, tc.y, EquateErrorMessage)
			actual := actualDiffString == ""
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", actualDiffString)
			}
		})
	}
}

func TestCompareRuntimObjectIgnoreRvTypeMeta(t *testing.T) {
	tests := []struct {
		name           string
		x              runtime.Object
		y              runtime.Object
		expectEquality bool
	}{
		{
			name:           "Different RV, equal",
			x:              &testimagestreamtagimportv1.TestImageStreamTagImport{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}},
			y:              &testimagestreamtagimportv1.TestImageStreamTagImport{},
			expectEquality: true,
		},
		{
			name: "Different obj and different RV, not equal",
			x:    &testimagestreamtagimportv1.TestImageStreamTagImport{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}},
			y:    &testimagestreamtagimportv1.TestImageStreamTagImport{ObjectMeta: metav1.ObjectMeta{Name: "other"}},
		},
		{
			name:           "Different TypeMeta, equal",
			x:              &testimagestreamtagimportv1.TestImageStreamTagImport{TypeMeta: metav1.TypeMeta{Kind: "Pod"}},
			y:              &testimagestreamtagimportv1.TestImageStreamTagImport{},
			expectEquality: true,
		},
		{
			name: "Different TypeMeta and object, not equal",
			x:    &testimagestreamtagimportv1.TestImageStreamTagImport{TypeMeta: metav1.TypeMeta{Kind: "Pod"}},
			y:    &testimagestreamtagimportv1.TestImageStreamTagImport{ObjectMeta: metav1.ObjectMeta{Name: "other"}},
		},
		{
			name: "Lists with items with different type meta and rv, equal",
			x: &testimagestreamtagimportv1.TestImageStreamTagImportList{Items: []testimagestreamtagimportv1.TestImageStreamTagImport{
				{TypeMeta: metav1.TypeMeta{Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}},
			}},
			y:              &testimagestreamtagimportv1.TestImageStreamTagImportList{Items: []testimagestreamtagimportv1.TestImageStreamTagImport{{}}},
			expectEquality: true,
		},
		{
			name: "Lists with different items, not equal",
			x: &testimagestreamtagimportv1.TestImageStreamTagImportList{Items: []testimagestreamtagimportv1.TestImageStreamTagImport{
				{Spec: testimagestreamtagimportv1.TestImageStreamTagImportSpec{ClusterName: "foo"}},
			}},
			y: &testimagestreamtagimportv1.TestImageStreamTagImportList{Items: []testimagestreamtagimportv1.TestImageStreamTagImport{{}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diff := cmp.Diff(tc.x, tc.y, RuntimeObjectIgnoreRvTypeMeta)
			if diff == "" != tc.expectEquality {
				t.Errorf("expectEquality: %t, got diff: %s", tc.expectEquality, diff)
			}
		})
	}
}
