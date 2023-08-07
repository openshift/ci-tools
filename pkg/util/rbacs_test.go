package util

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	coreapi "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCreateRBACs(t *testing.T) {
	testCases := []struct {
		id            string
		sa            *coreapi.ServiceAccount
		role          *rbacapi.Role
		roleBinding   *rbacapi.RoleBinding
		expectedError string
	}{
		{
			id: "happy",
			sa: &coreapi.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "ci-operator", Namespace: "test-namespace"}},
			role: &rbacapi.Role{
				ObjectMeta: metav1.ObjectMeta{Name: "ci-operator-image", Namespace: "test-namespace"},
				Rules: []rbacapi.PolicyRule{
					{
						APIGroups: []string{"", "image.openshift.io"},
						Resources: []string{"imagestreams/layers"},
						Verbs:     []string{"get", "update"},
					},
				},
			},
			roleBinding: &rbacapi.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ci-operator-image",
					Namespace: "test-namespace",
				},
				Subjects: []rbacapi.Subject{{Kind: "ServiceAccount", Name: "ci-operator", Namespace: "test-namespace"}},
				RoleRef: rbacapi.RoleRef{
					Kind: "Role",
					Name: "ci-operator-image",
				},
			},
		},
		{
			id: "sad",
			sa: &coreapi.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "ci-operator", Namespace: "test-namespace"}},
			role: &rbacapi.Role{
				ObjectMeta: metav1.ObjectMeta{Name: "ci-operator-image", Namespace: "test-namespace"},
				Rules: []rbacapi.PolicyRule{
					{
						APIGroups: []string{"", "image.openshift.io"},
						Resources: []string{"imagestreams/layers"},
						Verbs:     []string{"get", "update"},
					},
				},
			},
			roleBinding: &rbacapi.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ci-operator-image",
					Namespace: "test-namespace",
				},
				Subjects: []rbacapi.Subject{{Kind: "ServiceAccount", Name: "ci-operator", Namespace: "test-namespace"}},
				RoleRef: rbacapi.RoleRef{
					Kind: "Role",
					Name: "ci-operator-image",
				},
			},
			expectedError: `timeout while waiting for dockercfg secret creation for service account "ci-operator": timed out waiting for the condition`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()

			if tc.expectedError == "" {
				go func() {
					if err := wait.Poll(10*time.Millisecond, 100*time.Millisecond, func() (bool, error) {
						newSA := &coreapi.ServiceAccount{}
						if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{
							Namespace: "test-namespace",
							Name:      "ci-operator",
						}, newSA); err != nil {
							if apierrors.IsNotFound(err) {
								return false, nil
							}
							return false, fmt.Errorf("unexpected error trying to get the sa: %w", err)
						}
						newSA.ImagePullSecrets = append(newSA.ImagePullSecrets, v1.LocalObjectReference{Name: "ci-operator-dockercfg-12345"})
						if err := client.Update(context.Background(), newSA); err != nil {
							return false, fmt.Errorf("failed to update the SA: %w", err)
						}
						return true, nil
					}); err != nil {
						panic(fmt.Sprintf("failed to add image pull secret to sa: %v", err))
					}
				}()
			}

			if err := CreateRBACs(context.TODO(), tc.sa, tc.role, []rbacapi.RoleBinding{*tc.roleBinding}, client, 1*time.Millisecond, 100*time.Millisecond); err != nil {
				if !reflect.DeepEqual(err.Error(), tc.expectedError) {
					t.Fatalf("Expected: %v\nGot: %v", tc.expectedError, err.Error())
				}
			}
		})
	}
}
