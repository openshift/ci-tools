package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"

	templatev1 "github.com/openshift/api/template/v1"
	userv1 "github.com/openshift/api/user/v1"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := userv1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register userv1 scheme: %v", err))
	}
	if err := rbacv1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register rbacv1 scheme: %v", err))
	}
	if err := templatev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Errorf("failed to add templatev1 to scheme: %w", err))
	}
}

func TestNewYamlGroupCollector(t *testing.T) {

	testCases := []struct {
		name             string
		r                *yamlGroupCollector
		dir              string
		validateSubjects bool
		expected         sets.String
		expectedErr      error
	}{
		{
			name:     "base case",
			dir:      filepath.Join("testdata", "TestNewYamlGroupCollector", "base_case"),
			expected: sets.NewString("dedicated-admins", "group-from-cluster-role-binding", "group-from-k8s-list", "group-from-template"),
		},
		{
			name:             "cannot define group",
			validateSubjects: true,
			dir:              filepath.Join("testdata", "TestNewYamlGroupCollector", "invalid_1"),
			expectedErr:      fmt.Errorf("failed to walk dir: cannot create any group but found: ci-admins"),
		},
		{
			name:             "cannot use user in rolebinding",
			validateSubjects: true,
			dir:              filepath.Join("testdata", "TestNewYamlGroupCollector", "invalid_2"),
			expectedErr:      fmt.Errorf("failed to walk dir: cannot use User as subject in RoleBinding: admin-dedicated-admins"),
		},
		{
			name:             "cannot define group in template",
			validateSubjects: true,
			dir:              filepath.Join("testdata", "TestNewYamlGroupCollector", "invalid_3"),
			expectedErr:      fmt.Errorf("failed to walk dir: cannot create any group but found: ${team}-pool-admins"),
		},
		{
			name:             "cannot use params as subjects in the template",
			validateSubjects: true,
			dir:              filepath.Join("testdata", "TestNewYamlGroupCollector", "invalid_4"),
			expectedErr:      fmt.Errorf("failed to walk dir: cannot use ${ in a subject of RoleBinding: ${TEAM}-pool-admins"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := newYamlGroupCollector(tc.validateSubjects)
			actual, actualErr := r.collect(tc.dir)
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if actualErr == nil {
				if diff := cmp.Diff(tc.expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}
