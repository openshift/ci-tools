package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	userv1 "github.com/openshift/api/user/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/group"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := userv1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestMakeGroups(t *testing.T) {
	testCases := []struct {
		name                string
		openshiftPrivAdmins sets.String
		peribolosConfig     string
		mapping             map[string]string
		roverGroups         map[string][]string
		config              *group.Config
		clusters            sets.String
		expected            map[string]GroupClusters
		expectedErr         error
	}{
		{
			name:                "basic case",
			peribolosConfig:     "bar",
			openshiftPrivAdmins: sets.NewString("a"),
			mapping:             map[string]string{"a": "b", "c": "c"},
			roverGroups:         map[string][]string{"old-group-name": {"b", "c"}, "x": {"y", "y"}},
			config: &group.Config{
				ClusterGroups: map[string][]string{"cluster-group-1": {"build01", "build02"}},
				Groups: map[string]group.Target{
					"old-group-name": {
						RenameTo:      "new-group-name",
						ClusterGroups: []string{"cluster-group-1"},
					},
				},
			},
			clusters: sets.NewString("app.ci", "build01", "build02", "hive"),
			expected: map[string]GroupClusters{
				"openshift-priv-admins": {
					Clusters: sets.NewString("app.ci"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "openshift-priv-admins",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"b"},
					},
				},
				"a-group": {
					Clusters: sets.NewString("app.ci", "build01", "build02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "a-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"b"},
					},
				},
				"c-group": {
					Clusters: sets.NewString("app.ci", "build01", "build02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "c-group",
							Labels: map[string]string{"dptp.openshift.io/requester": "github-ldap-user-group-creator"},
						},
						Users: userv1.OptionalNames{"c"},
					},
				},
				"new-group-name": {
					Clusters: sets.NewString("build01", "build02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "new-group-name",
							Labels: map[string]string{"dptp.openshift.io/requester": "github-ldap-user-group-creator", "rover-group-name": "old-group-name"},
						},
						Users: userv1.OptionalNames{"b", "c"},
					},
				},
				"x": {
					Clusters: sets.NewString("app.ci", "build01", "build02", "hive"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "x",
							Labels: map[string]string{"dptp.openshift.io/requester": "github-ldap-user-group-creator"},
						},
						Users: userv1.OptionalNames{"y"},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := makeGroups(tc.openshiftPrivAdmins, tc.peribolosConfig,
				tc.mapping, tc.roverGroups, tc.config, tc.clusters)
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if actualErr == nil {
				if diff := cmp.Diff(tc.expected, actual); diff != "" {
					t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
				}
			}
		})
	}
}

func TestEnsureGroups(t *testing.T) {

	g01 := &userv1.Group{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "gh01-group",
			Labels: map[string]string{api.DPTPRequesterLabel: toolName},
		},
		Users: userv1.OptionalNames{"bar"},
	}
	g03 := &userv1.Group{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "gh03-group",
			Labels: map[string]string{api.DPTPRequesterLabel: toolName},
		},
	}

	testCases := []struct {
		name       string
		clients    map[string]ctrlruntimeclient.Client
		groups     map[string]GroupClusters
		dryRun     bool
		expected   error
		verifyFunc func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error
	}{
		{
			name: "basic case",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewFakeClient(g01.DeepCopy()),
				"b02": fakeclient.NewFakeClient(g03.DeepCopy()),
			},
			groups: map[string]GroupClusters{
				"gh01-group": {
					Clusters: sets.NewString("b01", "b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh01-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"gh01", "k01"},
					},
				},
				"gh02-group": {
					Clusters: sets.NewString("b01", "b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh02-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"gh02", "k02"},
					},
				},
			},
			verifyFunc: func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error {
				for cluster, client := range clients {
					for i := 1; i <= 2; i++ {
						actual := &userv1.Group{}
						if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: fmt.Sprintf("gh0%d-group", i)}, actual); err != nil {
							return err
						}
						expected := &userv1.Group{
							ObjectMeta: metav1.ObjectMeta{
								Name:   fmt.Sprintf("gh0%d-group", i),
								Labels: map[string]string{api.DPTPRequesterLabel: toolName},
							},
							Users: userv1.OptionalNames{fmt.Sprintf("gh0%d", i), fmt.Sprintf("k0%d", i)},
						}
						if diff := cmp.Diff(expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
							return fmt.Errorf("%s: actual does not match expected, diff: %s", cluster, diff)
						}
						if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "gh03-group"}, &userv1.Group{}); !errors.IsNotFound(err) {
							return fmt.Errorf("gh03-group is not deleted")
						}
					}
				}
				return nil
			},
		},
		{
			name: "basic case: dryRun=true",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewFakeClient(g01.DeepCopy()),
				"b02": fakeclient.NewFakeClient(g03.DeepCopy()),
			},
			groups: map[string]GroupClusters{
				"gh01-group": {
					Clusters: sets.NewString("b01", "b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh01-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"gh01", "k01"},
					},
				},
				"gh02-group": {
					Clusters: sets.NewString("b01", "b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh02-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"gh02", "k02"},
					},
				},
			},
			dryRun: true,
			verifyFunc: func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error {
				b01Client := clients["b01"]
				actual := &userv1.Group{}
				if err := b01Client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "gh01-group"}, actual); err != nil {
					return err
				}
				expected := &userv1.Group{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "gh01-group",
						Labels: map[string]string{api.DPTPRequesterLabel: toolName},
					},
					Users: userv1.OptionalNames{"bar"},
				}
				if diff := cmp.Diff(expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("%s: actual does not match expected, diff: %s", "b01", diff)
				}
				b02Client := clients["b02"]
				if err := b02Client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "gh03-group"}, &userv1.Group{}); err != nil {
					return err
				}
				return nil
			},
		},
		{
			name: "invalid group: duplicate members",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewFakeClient(),
			},
			groups: map[string]GroupClusters{
				"gh01-group": {
					Clusters: sets.NewString("b01", "b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh01-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"a", "a"},
					},
				},
			},
			dryRun: true,
			expected: kerrors.NewAggregate([]error{fmt.Errorf("attempt to create invalid group gh01-group on cluster b01: duplicate member: a"),
				fmt.Errorf("attempt to create invalid group gh01-group on cluster b02: duplicate member: a")}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			actual := ensureGroups(ctx, tc.clients, tc.groups, 60, tc.dryRun)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if actual == nil && tc.verifyFunc != nil {
				if err := tc.verifyFunc(ctx, tc.clients); err != nil {
					t.Errorf("%s: unexpected error occurred: %v", tc.name, err)
				}
			}
		})
	}
}
