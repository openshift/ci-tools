package secretsyncer

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/controller/secretsyncer/config"
)

func TestMirrorSecret(t *testing.T) {
	const cluster = "some-cluster"
	defaultSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "src"},
		Data:       map[string][]byte{"test_key": []byte("test_value")},
	}
	for _, tc := range []struct {
		name         string
		config       config.Configuration
		src          corev1.Secret
		targetFilter filter
		configMutate func(*config.Configuration)
		expectedData map[string][]byte
		shouldErr    bool
	}{
		{
			name: "empty src is ignored",
			src:  corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "src"}},
		},
		{
			name:         "normal secret is copied",
			src:          defaultSecret,
			expectedData: map[string][]byte{"test_key": []byte("test_value")},
		},
		{
			name:         "filter is respected",
			src:          defaultSecret,
			targetFilter: func(_ *boostrapSecretConfigTarget) bool { return false },
		},
		{
			name:      "error is reported",
			src:       defaultSecret,
			shouldErr: true,
		},
		{
			name: "Other fields not present in source secret are kept",
			src: func() corev1.Secret {
				s := defaultSecret.DeepCopy()
				s.Data["something-else"] = []byte("some-val")
				return *s
			}(),
			expectedData: map[string][]byte{
				"test_key":       []byte("test_value"),
				"something-else": []byte("some-val"),
			},
		},
		{
			name:         "Secret with matching cluster is copied",
			src:          defaultSecret,
			configMutate: func(c *config.Configuration) { c.Secrets[0].To.Cluster = utilpointer.StringPtr(cluster) },
			expectedData: map[string][]byte{"test_key": []byte("test_value")},
		},
		{
			name:         "Secret with non-matching cluster is not copied",
			src:          defaultSecret,
			configMutate: func(c *config.Configuration) { c.Secrets[0].To.Cluster = utilpointer.StringPtr("something else") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.targetFilter == nil {
				tc.targetFilter = func(_ *boostrapSecretConfigTarget) bool { return true }
			}
			targetClient := &potentiallyCreateErroringClient{Client: fakectrlruntimeclient.NewFakeClient()}
			if tc.shouldErr {
				targetClient.err = errors.New("injected error")
			}
			configuration := config.Configuration{

				Secrets: []config.MirrorConfig{
					{
						From: config.SecretLocation{Namespace: "test-ns", Name: "src"},
						To:   config.SecretLocationWithCluster{SecretLocation: config.SecretLocation{Namespace: "test-ns", Name: "dst"}},
					},
				},
			}
			if tc.configMutate != nil {
				tc.configMutate(&configuration)
			}
			ca := &config.Agent{}
			ca.Set(&configuration)

			req := requestForCluster(cluster, "test-ns", "src")
			r := &reconciler{
				ctx:                    context.Background(),
				config:                 ca.Config,
				referenceClusterClient: fakectrlruntimeclient.NewFakeClient(&tc.src),
				clients:                map[string]ctrlruntimeclient.Client{"some-cluster": targetClient},
				targetFilter:           tc.targetFilter,
			}
			if err := r.reconcile(logrus.NewEntry(logrus.New()), req); err != nil != tc.shouldErr {
				t.Fatalf("shouldErr is %t, got %v", tc.shouldErr, err)
			}
			if len(tc.expectedData) == 0 {
				return
			}
			var actualResult corev1.Secret
			if err := targetClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "dst"}, &actualResult); err != nil {
				t.Fatalf("failed to get secret: %v", err)
			}
			if diff := cmp.Diff(tc.expectedData, actualResult.Data); diff != "" {
				t.Errorf("expected data differs from actual data: %s", diff)
			}
		})
	}
}

type potentiallyCreateErroringClient struct {
	ctrlruntimeclient.Client
	err error
}

func (pcec *potentiallyCreateErroringClient) Create(ctx context.Context, obj runtime.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if pcec.err != nil {
		return pcec.err
	}
	return pcec.Client.Create(ctx, obj, opts...)
}

func TestFilter(t *testing.T) {
	target := boostrapSecretConfigTarget{cluster: "cluster", namespace: "namespace", name: "name", key: "key"}
	testCases := []struct {
		name string
		cfg  secretbootstrap.Config

		expectedResult bool
	}{
		{
			name: "forbidden",
			cfg: secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{
				From: map[string]secretbootstrap.BitWardenContext{target.key: {}},
				To: []secretbootstrap.SecretContext{
					{Cluster: target.cluster, Namespace: target.namespace, Name: target.name},
				},
			}}},
			expectedResult: false,
		},
		{
			name:           "allowed",
			expectedResult: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if result := filterFromConfig(tc.cfg)(&target); result != tc.expectedResult {
				t.Errorf("expected result %t, got result %t", tc.expectedResult, result)
			}
		})
	}
}
