package secretsyncer

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/controller/secretsyncer/config"
)

func TestMirrorSecret(t *testing.T) {
	configuration := config.Configuration{
		Secrets: []config.MirrorConfig{
			{
				From: config.SecretLocation{Namespace: "test-ns", Name: "src"},
				To:   config.SecretLocation{Namespace: "test-ns", Name: "dst"},
			},
		},
	}
	defaultSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "src"},
		Data:       map[string][]byte{"test_key": []byte("test_value")},
	}
	for _, tc := range []struct {
		name                  string
		config                config.Configuration
		src                   corev1.Secret
		targetFilter          filter
		shouldCopy, shouldErr bool
	}{
		{
			name: "empty src is ignored",
			src:  corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "src"}},
		},
		{
			name:       "normal secret is copied",
			src:        defaultSecret,
			shouldCopy: true,
		},
		{
			name:         "filter is respected",
			src:          defaultSecret,
			targetFilter: func(_ string, _ types.NamespacedName) bool { return false },
			shouldCopy:   false,
		},
		{
			name:      "error is reported",
			src:       defaultSecret,
			shouldErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.targetFilter == nil {
				tc.targetFilter = func(_ string, _ types.NamespacedName) bool { return true }
			}
			targetClient := &potentiallyCreateErroringClient{Client: fakectrlruntimeclient.NewFakeClient()}
			if tc.shouldErr {
				targetClient.err = errors.New("injected error")
			}
			ca := &config.Agent{}
			ca.Set(&configuration)
			req := requestForCluster("some-cluster", "test-ns", "src")
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
			exists := !apierrors.IsNotFound(targetClient.Get(r.ctx, types.NamespacedName{Namespace: "test-ns", Name: "dst"}, &corev1.Secret{}))
			if exists != tc.shouldCopy {
				t.Errorf("expected secret to exist: %t did exist: %t", tc.shouldCopy, exists)
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
	const cluster = "cluster"
	request := types.NamespacedName{Namespace: "namespace", Name: "Name"}
	testCases := []struct {
		name string
		cfg  secretbootstrap.Config

		expectedResult bool
	}{
		{
			name: "forbidden",
			cfg: secretbootstrap.Config{{To: []secretbootstrap.SecretContext{
				{Cluster: cluster, Namespace: request.Namespace, Name: request.Name},
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
			if result := filterFromConfig(tc.cfg)(cluster, request); result != tc.expectedResult {
				t.Errorf("expected result %t, got result %t", tc.expectedResult, result)
			}
		})
	}
}
