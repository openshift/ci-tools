package util

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestEnsureImagePullSecret(t *testing.T) {
	ctx := context.Background()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "ci",
			Name:        "registry-pull-credentials",
			Annotations: map[string]string{"a": "c"},
		},
		Data: map[string][]byte{"pass": []byte("some")},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	targetSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "some-ns",
			Name:        "registry-pull-credentials",
			Annotations: map[string]string{"a": "b"},
		},
		Data: map[string][]byte{"pass": []byte("other")},
	}

	testCases := []struct {
		name      string
		client    ctrlruntimeclient.Client
		secret    *corev1.Secret
		namespace string
		expected  error
		verify    func(client ctrlruntimeclient.Client) error
	}{
		{
			name:      "basic case: create",
			client:    fakeclient.NewClientBuilder().WithRuntimeObjects(secret.DeepCopy()).Build(),
			namespace: "some-ns",
			verify: func(client ctrlruntimeclient.Client) error {
				actualSecret := &corev1.Secret{}
				if err := client.Get(ctx, types.NamespacedName{Name: "registry-pull-credentials", Namespace: "some-ns"}, actualSecret); err != nil {
					return err
				}
				expectedSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "some-ns",
						Name:        "registry-pull-credentials",
						Annotations: map[string]string{"a": "c"},
					},
					Data: map[string][]byte{"pass": []byte("some")},
					Type: "kubernetes.io/dockerconfigjson",
				}

				if diff := cmp.Diff(expectedSecret, actualSecret, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:      "basic case: update",
			client:    fakeclient.NewClientBuilder().WithRuntimeObjects(secret.DeepCopy(), targetSecret.DeepCopy()).Build(),
			namespace: "some-ns",
			verify: func(client ctrlruntimeclient.Client) error {
				actualSecret := &corev1.Secret{}
				if err := client.Get(ctx, types.NamespacedName{Name: "registry-pull-credentials", Namespace: "some-ns"}, actualSecret); err != nil {
					return err
				}
				expectedSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "some-ns",
						Name:        "registry-pull-credentials",
						Annotations: map[string]string{"a": "c"},
					},
					Data: map[string][]byte{"pass": []byte("some")},
					Type: "kubernetes.io/dockerconfigjson",
				}

				if diff := cmp.Diff(expectedSecret, actualSecret, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:      "attempt to copy to ci",
			client:    fakeclient.NewClientBuilder().Build(),
			namespace: "ci",
			verify: func(client ctrlruntimeclient.Client) error {
				actualSecret := &corev1.Secret{}
				if err := client.Get(ctx, types.NamespacedName{Name: "registry-pull-credentials", Namespace: "ci"}, actualSecret); !kerrors.IsNotFound(err) {
					return fmt.Errorf("the expected NotFound error did not occur")
				}
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := EnsureImagePullSecret(ctx, tc.namespace, tc.client, logrus.WithField("tc.name", tc.name))
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if actual == nil && tc.verify != nil {
				if err := tc.verify(tc.client); err != nil {
					t.Errorf("unexpcected error: %v", err)
				}
			}
		})
	}
}
