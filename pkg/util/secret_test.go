package util

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

// interceptingClient wraps a ctrlruntimeclient.Client and allows injecting
// errors for specific operations to simulate race conditions in tests.
type interceptingClient struct {
	ctrlruntimeclient.Client
	mu             sync.Mutex
	createErrors   []error // errors to return on successive Create calls; nil entries fall through to the real client
	getErrors      []error // errors to return on successive Get calls; nil entries fall through to the real client
	createCallCount int
	getCallCount    int
}

func (c *interceptingClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	c.mu.Lock()
	idx := c.createCallCount
	c.createCallCount++
	c.mu.Unlock()
	if idx < len(c.createErrors) && c.createErrors[idx] != nil {
		return c.createErrors[idx]
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *interceptingClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	c.mu.Lock()
	idx := c.getCallCount
	c.getCallCount++
	c.mu.Unlock()
	if idx < len(c.getErrors) && c.getErrors[idx] != nil {
		return c.getErrors[idx]
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

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
		{
			name: "race condition: Create returns AlreadyExists then Get returns NotFound, retry succeeds",
			client: &interceptingClient{
				Client: fakeclient.NewClientBuilder().Build(),
				// First Create: AlreadyExists (simulates leftover secret from prior job)
				// Second Create: falls through to real client (succeeds because secret is gone)
				createErrors: []error{
					kerrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, "pull-secret"),
					nil,
				},
				// First Get: NotFound (simulates concurrent cleanup deleting the secret)
				getErrors: []error{
					kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "pull-secret"),
				},
			},
			expected: true,
			verify: func(client ctrlruntimeclient.Client) error {
				// The interceptingClient wraps the real client, so verify via Get on the wrapper
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
			name: "race condition exhausts retries",
			client: &interceptingClient{
				Client: fakeclient.NewClientBuilder().Build(),
				// All Creates return AlreadyExists
				createErrors: []error{
					kerrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, "pull-secret"),
					kerrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, "pull-secret"),
					kerrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, "pull-secret"),
				},
				// All Gets return NotFound
				getErrors: []error{
					kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "pull-secret"),
					kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "pull-secret"),
					kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "pull-secret"),
				},
			},
			expectedError: fmt.Errorf("failed to upsert secret ns/pull-secret after 3 attempts due to concurrent modifications"),
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
