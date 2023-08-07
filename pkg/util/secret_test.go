package util

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestUpsertImmutableSecret(t *testing.T) {

	srcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "pull-secret",
			Labels:    map[string]string{"dptp.openshift.io/requester": "foo"},
		},
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("xyz")},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	srcSecretWithDiffLabel := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "pull-secret",
			Labels:    map[string]string{"dptp.openshift.io/requester": "foo"},
		},
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("abc")},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	dstSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "pull-secret",
			Labels:    map[string]string{"dptp.openshift.io/requester": "bar"},
		},
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("abc")},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	im := true

	testCases := []struct {
		name          string
		client        ctrlruntimeclient.Client
		expected      bool
		expectedError error
		verify        func(ctrlruntimeclient.Client) error
	}{
		{
			name:     "the target secret is created",
			client:   fakeclient.NewClientBuilder().Build(),
			expected: true,
			verify: func(client ctrlruntimeclient.Client) error {
				actual := &corev1.Secret{}
				if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: "ns", Name: "pull-secret"}, actual); err != nil {
					return err
				}
				expected := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "pull-secret",
						Labels:    map[string]string{api.DPTPRequesterLabel: "bar"},
					},
					Data:      map[string][]byte{corev1.DockerConfigJsonKey: []byte("abc")},
					Type:      corev1.SecretTypeDockerConfigJson,
					Immutable: &im,
				}
				if diff := cmp.Diff(expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:   "the target secret is update",
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(srcSecret.DeepCopy()).Build(),
			verify: func(client ctrlruntimeclient.Client) error {
				actual := &corev1.Secret{}
				if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: "ns", Name: "pull-secret"}, actual); err != nil {
					return err
				}
				expected := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "pull-secret",
						Labels:    map[string]string{api.DPTPRequesterLabel: "bar"},
					},
					Data:      map[string][]byte{corev1.DockerConfigJsonKey: []byte("abc")},
					Type:      corev1.SecretTypeDockerConfigJson,
					Immutable: &im,
				}
				if diff := cmp.Diff(expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:   "labels are ignored",
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(srcSecretWithDiffLabel.DeepCopy()).Build(),
			verify: func(client ctrlruntimeclient.Client) error {
				actual := &corev1.Secret{}
				if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: "ns", Name: "pull-secret"}, actual); err != nil {
					return err
				}
				expected := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "pull-secret",
						Labels:    map[string]string{api.DPTPRequesterLabel: "foo"},
					},
					Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("abc")},
					Type: corev1.SecretTypeDockerConfigJson,
				}
				if diff := cmp.Diff(expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			// Needed so the racedetector tells us if we accidentally re-use global state, e.G. by not deepcopying
			t.Parallel()

			actual, actualError := UpsertImmutableSecret(context.TODO(), tc.client, dstSecret.DeepCopy())
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}

			if tc.verify != nil {
				if err := tc.verify(tc.client); err != nil {
					t.Errorf("%s: an unexpected error occurred: %v", tc.name, err)
				}
			}
		})
	}
}
