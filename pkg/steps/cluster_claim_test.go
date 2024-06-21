package steps

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func aClusterPool() *hivev1.ClusterPool {
	return &hivev1.ClusterPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-ocp-4.7.0-amd64-aws-us-east-1",
			Namespace: "ci-cluster-pool",
			Labels: map[string]string{
				"product":      "ocp",
				"version":      "4.7.0",
				"architecture": "amd64",
				"cloud":        "aws",
				"owner":        "dpp",
				"region":       "us-east-1",
			},
		},
	}
}

func aClusterDeployment() *hivev1.ClusterDeployment {
	return &hivev1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-ocp-4.7.0-amd64-aws-us-east-1-ccx23",
			Namespace: "ci-ocp-4.7.0-amd64-aws-us-east-1-ccx23",
		},
		Spec: hivev1.ClusterDeploymentSpec{
			ClusterMetadata: &hivev1.ClusterMetadata{
				AdminKubeconfigSecretRef: corev1.LocalObjectReference{
					Name: "ci-openshift-46-aws-us-east-1-ccx23-0-gpjsf-admin-kubeconfig",
				},
				AdminPasswordSecretRef: &corev1.LocalObjectReference{
					Name: "ci-openshift-46-aws-us-east-1-ccx23-0-gpjsf-admin-password",
				},
			},
		},
	}
}

func aKubeconfigSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-openshift-46-aws-us-east-1-ccx23-0-gpjsf-admin-kubeconfig",
			Namespace: "ci-ocp-4.7.0-amd64-aws-us-east-1-ccx23",
		},
		Data: map[string][]byte{
			"kubeconfig": []byte("some-kubeconfig"),
		},
	}
}

func aPasswordSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-openshift-46-aws-us-east-1-ccx23-0-gpjsf-admin-password",
			Namespace: "ci-ocp-4.7.0-amd64-aws-us-east-1-ccx23",
		},
		Data: map[string][]byte{
			"password": []byte("some-kubeadmin-password"),
		},
	}
}

func init() {
	if err := hivev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add hivev1 to scheme: %v", err))
	}
}

func TestClusterClaimStepAcquireCluster(t *testing.T) {
	testCases := []struct {
		name          string
		clusterClaim  *api.ClusterClaim
		jobSpec       *api.JobSpec
		hiveClient    ctrlruntimeclient.WithWatch
		client        loggingclient.LoggingClient
		waitForClaim  func(client ctrlruntimeclient.WithWatch, ns, name string, claim *hivev1.ClusterClaim, timeout time.Duration) error
		expected      *hivev1.ClusterClaim
		expectedError error
		verifyFunc    func(client ctrlruntimeclient.Client) error
	}{
		{
			name: "happy path",
			clusterClaim: &api.ClusterClaim{
				Product:      api.ReleaseProductOCP,
				Version:      "4.7.0",
				Architecture: api.ReleaseArchitectureAMD64,
				Cloud:        api.CloudAWS,
				Owner:        "dpp",
				Timeout:      &prowv1.Duration{Duration: time.Hour},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					ProwJobID: "c2a971b7-947b-11eb-9747-0a580a820213",
					BuildID:   "1378330119495487488",
					Job:       "pull-ci-openshift-console-master-images",
				},
			},
			hiveClient: bcc(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(aClusterPool()).Build(), func(client *clusterClaimStatusSettingClient) {
				client.namespace = "ci-ocp-4.7.0-amd64-aws-us-east-1-ccx23"
				client.conditionStatus = corev1.ConditionTrue
			}),
			client: loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects().Build()),
			waitForClaim: func(client ctrlruntimeclient.WithWatch, ns, name string, claim *hivev1.ClusterClaim, timeout time.Duration) error {
				return client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, claim)
			},
			expected: &hivev1.ClusterClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "c2a971b7-947b-11eb-9747-0a580a820213",
					Namespace: "ci-cluster-pool",
					Labels: map[string]string{
						"prow.k8s.io/build-id": "1378330119495487488",
						"prow.k8s.io/job":      "pull-ci-openshift-console-master-images",
					},
				},
				Spec: hivev1.ClusterClaimSpec{
					ClusterPoolName: "ci-ocp-4.7.0-amd64-aws-us-east-1",
					Namespace:       "ci-ocp-4.7.0-amd64-aws-us-east-1-ccx23",
					Lifetime: &metav1.Duration{
						Duration: 4 * time.Hour,
					},
				},
				Status: hivev1.ClusterClaimStatus{
					Conditions: []hivev1.ClusterClaimCondition{
						{
							Type:   hivev1.ClusterRunningCondition,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			verifyFunc: func(client ctrlruntimeclient.Client) error {
				ctx := context.TODO()
				actualSecret := &corev1.Secret{}
				if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "as-hive-admin-kubeconfig", Namespace: "ci-op-test"}, actualSecret); err != nil {
					return err
				}
				immutable := true
				expectedSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "as-hive-admin-kubeconfig",
						Namespace: "ci-op-test",
					},
					Data: map[string][]byte{
						"kubeconfig": []byte("some-kubeconfig"),
					},
					Immutable: &immutable,
				}
				if diff := cmp.Diff(expectedSecret, actualSecret, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				actualSecret = &corev1.Secret{}
				if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "as-hive-admin-password", Namespace: "ci-op-test"}, actualSecret); err != nil {
					return err
				}
				expectedSecret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "as-hive-admin-password",
						Namespace: "ci-op-test",
					},
					Data: map[string][]byte{
						"password": []byte("some-kubeadmin-password"),
					},
					Immutable: &immutable,
				}
				if diff := cmp.Diff(expectedSecret, actualSecret, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name: "no matching pools",
			clusterClaim: &api.ClusterClaim{
				Product:      api.ReleaseProductOCP,
				Version:      "4.6.0",
				Architecture: api.ReleaseArchitectureAMD64,
				Cloud:        api.CloudAWS,
				Owner:        "dpp",
				Timeout:      &prowv1.Duration{Duration: time.Hour},
			},
			hiveClient:    bcc(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(aClusterPool()).Build()),
			client:        loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects().Build()),
			jobSpec:       &api.JobSpec{},
			expectedError: fmt.Errorf("failed to find a cluster pool providing the cluster: map[architecture:amd64 cloud:aws owner:dpp product:ocp version:4.6.0]"),
			verifyFunc: func(client ctrlruntimeclient.Client) error {
				ctx := context.TODO()
				actualSecret := &corev1.Secret{}
				if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "hive-admin-kubeconfig", Namespace: "ci-op-test"}, actualSecret); !apierrors.IsNotFound(err) {
					return fmt.Errorf("expecting not found error, but it is %w", err)
				}
				if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "hive-admin-password", Namespace: "ci-op-test"}, actualSecret); !apierrors.IsNotFound(err) {
					return fmt.Errorf("expecting not found error, but it is %w", err)
				}
				return nil
			},
		},
		{
			name: "timeout",
			clusterClaim: &api.ClusterClaim{
				Product:      api.ReleaseProductOCP,
				Version:      "4.7.0",
				Architecture: api.ReleaseArchitectureAMD64,
				Cloud:        api.CloudAWS,
				Owner:        "dpp",
				Timeout:      &prowv1.Duration{Duration: time.Second},
			},
			hiveClient: bcc(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(aClusterPool()).Build(), func(client *clusterClaimStatusSettingClient) {
				client.namespace = "ci-ocp-4.7.0-amd64-aws-us-east-1-ccx23"
				client.conditionStatus = corev1.ConditionFalse
			}),
			client: loggingclient.New(fakectrlruntimeclient.NewClientBuilder().Build()),
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					ProwJobID: "c2a971b7-947b-11eb-9747-0a580a820213",
					BuildID:   "1378330119495487488",
					Job:       "pull-ci-openshift-console-master-images",
				},
			},
			waitForClaim: func(client ctrlruntimeclient.WithWatch, ns, name string, claim *hivev1.ClusterClaim, timeout time.Duration) error {
				return wait.ErrWaitTimeout
			},
			expected: &hivev1.ClusterClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "c2a971b7-947b-11eb-9747-0a580a820213",
					Namespace: "ci-cluster-pool",
					Labels: map[string]string{
						"prow.k8s.io/build-id": "1378330119495487488",
						"prow.k8s.io/job":      "pull-ci-openshift-console-master-images",
					},
				},
				Spec: hivev1.ClusterClaimSpec{
					ClusterPoolName: "ci-ocp-4.7.0-amd64-aws-us-east-1",
					Namespace:       "ci-ocp-4.7.0-amd64-aws-us-east-1-ccx23",
					Lifetime: &metav1.Duration{
						Duration: 4 * time.Hour,
					},
				},
				Status: hivev1.ClusterClaimStatus{
					Conditions: []hivev1.ClusterClaimCondition{
						{
							Type:   hivev1.ClusterRunningCondition,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			expectedError: fmt.Errorf("failed to wait for the created cluster claim to become ready: %w", wait.ErrWaitTimeout),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := clusterClaimStep{
				as:           "as",
				clusterClaim: tc.clusterClaim,
				client:       tc.client,
				hiveClient:   tc.hiveClient,
				jobSpec:      tc.jobSpec,
			}
			if tc.jobSpec != nil {
				tc.jobSpec.SetNamespace("ci-op-test")
			}
			actual, actualError := s.acquireCluster(context.TODO(), tc.waitForClaim)
			if diff := cmp.Diff(tc.expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if tc.verifyFunc != nil {
				if err := tc.verifyFunc(tc.client); err != nil {
					t.Errorf("%s: an unexpected error occurred: %v", tc.name, err)
				}
			}
		})
	}
}

func bcc(upstream ctrlruntimeclient.WithWatch, opts ...func(*clusterClaimStatusSettingClient)) ctrlruntimeclient.WithWatch {
	c := &clusterClaimStatusSettingClient{
		WithWatch: upstream,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type clusterClaimStatusSettingClient struct {
	ctrlruntimeclient.WithWatch
	namespace       string
	conditionStatus corev1.ConditionStatus
}

func (client *clusterClaimStatusSettingClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if asserted, match := obj.(*hivev1.ClusterClaim); match && client.namespace != "" {
		asserted.Spec.Namespace = client.namespace
		asserted.Status.Conditions = []hivev1.ClusterClaimCondition{
			{
				Type:   hivev1.ClusterRunningCondition,
				Status: client.conditionStatus,
			},
		}
		for _, obj := range []ctrlruntimeclient.Object{aClusterDeployment(), aKubeconfigSecret(), aPasswordSecret()} {
			if err := client.WithWatch.Create(ctx, obj); err != nil {
				return err
			}
		}
	}
	return client.WithWatch.Create(ctx, obj, opts...)
}
