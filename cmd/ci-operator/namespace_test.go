package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	rbacapi "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGenerateAuthorAccessRoleBinding(t *testing.T) {
	testCases := []struct {
		id       string
		authors  []string
		expected *rbacapi.RoleBinding
	}{
		{
			id:      "basic case",
			authors: []string{"a", "e"},
			expected: &rbacapi.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ci-op-author-access",
					Namespace: "ci-op-xxxx",
				},
				Subjects: []rbacapi.Subject{{Kind: "Group", Name: "a-group"}, {Kind: "Group", Name: "e-group"}},
				RoleRef: rbacapi.RoleRef{
					Kind: "ClusterRole",
					Name: "admin",
				},
			},
		},
		{
			id:      "no duplicated authors",
			authors: []string{"a", "a"},
			expected: &rbacapi.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ci-op-author-access",
					Namespace: "ci-op-xxxx",
				},
				Subjects: []rbacapi.Subject{{Kind: "Group", Name: "a-group"}},
				RoleRef: rbacapi.RoleRef{
					Kind: "ClusterRole",
					Name: "admin",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			actual := generateAuthorAccessRoleBinding("ci-op-xxxx", tc.authors)
			if diff := cmp.Diff(tc.expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
