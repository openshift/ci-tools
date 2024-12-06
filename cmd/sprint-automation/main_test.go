package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := configv1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add configv1 to scheme: %v", err))
	}
}

func TestUpgradeBuild02(t *testing.T) {
	now := time.Now()
	T23HoursAgo := metav1.NewTime(now.Add(-23 * time.Hour))
	T26HoursAgo := metav1.NewTime(now.Add(-26 * time.Hour))
	T8DaysAgo := metav1.NewTime(now.Add(-24 * 8 * time.Hour))

	testCases := []struct {
		name        string
		b01Client   ctrlruntimeclient.Client
		b02Client   ctrlruntimeclient.Client
		expected    *versionInfo
		expectedErr error
	}{
		{
			name: "to 4.9.6",
			b01Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T26HoursAgo,
								Version:        "4.9.6",
								State:          configv1.CompletedUpdate,
							},
							{
								Version: "4.9.5",
							},
						},
					},
				},
			).Build(),
			b02Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T26HoursAgo,
								Version:        "4.9.3",
								State:          configv1.CompletedUpdate,
							},
						},
					},
				},
			).Build(),
			expected: &versionInfo{
				version:        "4.9.6",
				stableDuration: "1 day",
				state:          "Completed",
			},
		},
		{
			name: "build02 is up2date",
			b01Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T26HoursAgo,
								Version:        "4.9.6",
								State:          configv1.CompletedUpdate,
							},
							{
								Version: "4.9.5",
							},
						},
					},
				},
			).Build(),
			b02Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T26HoursAgo,
								Version:        "4.9.6",
							},
						},
					},
				},
			).Build(),
		},
		{
			name: "build01 is soaking after z-stream upgrade",
			b01Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T23HoursAgo,
								Version:        "4.9.6",
								State:          configv1.CompletedUpdate,
							},
							{
								Version: "4.9.5",
							},
						},
					},
				},
			).Build(),
			b02Client: fakectrlruntimeclient.NewClientBuilder().Build(),
		},
		{
			name: "build01 is soaking after y-stream upgrade",
			b01Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T23HoursAgo,
								Version:        "4.9.6",
								State:          configv1.CompletedUpdate,
							},
							{
								Version: "4.8.18",
							},
						},
					},
				},
			).Build(),
			b02Client: fakectrlruntimeclient.NewClientBuilder().Build(),
		},
		{
			name: "build02 is upgraded after build01's y-stream upgrade",
			b01Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T8DaysAgo,
								Version:        "4.9.5",
								State:          configv1.CompletedUpdate,
							},
							{
								Version: "4.8.18",
							},
						},
					},
				},
			).Build(),
			b02Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T26HoursAgo,
								Version:        "4.8.17",
								State:          configv1.CompletedUpdate,
							},
						},
					},
				},
			).Build(),
			expected: &versionInfo{
				version:        "4.9.5",
				stableDuration: "7 days",
				state:          "Completed",
			},
		},
		{
			name: "build02 is newer than build01",
			b01Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T8DaysAgo,
								Version:        "4.9.6",
								State:          configv1.CompletedUpdate,
							},
							{
								Version: "4.9.5",
							},
						},
					},
				},
			).Build(),
			b02Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								Version: "4.9.17",
								State:   configv1.CompletedUpdate,
							},
						},
					},
				},
			).Build(),
		},
		{
			name: "upgrade of build02 is still ongoing",
			b01Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								CompletionTime: &T8DaysAgo,
								Version:        "4.9.6",
								State:          configv1.CompletedUpdate,
							},
							{
								Version: "4.9.5",
							},
						},
					},
				},
			).Build(),
			b02Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								Version: "4.9.3",
								State:   configv1.PartialUpdate,
							},
						},
					},
				},
			).Build(),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := upgradeBuild02(context.TODO(), tc.b01Client, tc.b02Client)
			if diff := cmp.Diff(tc.expected, actual, cmp.Comparer(func(x, y versionInfo) bool {
				return cmp.Diff(x.version, y.version) == "" &&
					cmp.Diff(x.stableDuration, y.stableDuration) == "" &&
					cmp.Diff(x.state, y.state) == ""
			})); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}
