package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

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
		name                    string
		skipOpenshiftPrivAdmins sets.Set[string]
		openshiftPrivAdmins     sets.Set[string]
		peribolosConfig         string
		mapping                 map[string]string
		roverGroups             map[string][]string
		config                  *group.Config
		clusters                sets.Set[string]
		expected                map[string]GroupClusters
		expectedErr             error
	}{
		{
			name:                    "basic case",
			peribolosConfig:         "bar",
			skipOpenshiftPrivAdmins: sets.New("RH-Cachito"),
			openshiftPrivAdmins:     sets.New("a", "RH-Cachito"),
			mapping:                 map[string]string{"a": "b", "c": "c"},
			roverGroups:             map[string][]string{"old-group-name": {"b", "c"}, "x": {"y", "y"}},
			config: &group.Config{
				ClusterGroups: map[string][]string{"cluster-group-1": {"build01", "build02"}},
				Groups: map[string]group.Target{
					"old-group-name": {
						RenameTo:      "new-group-name",
						ClusterGroups: []string{"cluster-group-1"},
					},
				},
			},
			clusters: sets.New[string]("app.ci", "build01", "build02", "hosted-mgmt"),
			expected: map[string]GroupClusters{
				"openshift-priv-admins": {
					Clusters: sets.New[string]("app.ci"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "openshift-priv-admins",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"b"},
					},
				},
				"a-group": {
					Clusters: sets.New[string]("app.ci", "build01", "build02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "a-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"b"},
					},
				},
				"c-group": {
					Clusters: sets.New[string]("app.ci", "build01", "build02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "c-group",
							Labels: map[string]string{"dptp.openshift.io/requester": "github-ldap-user-group-creator"},
						},
						Users: userv1.OptionalNames{"c"},
					},
				},
				"new-group-name": {
					Clusters: sets.New[string]("build01", "build02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "new-group-name",
							Labels: map[string]string{"dptp.openshift.io/requester": "github-ldap-user-group-creator", "rover-group-name": "old-group-name"},
						},
						Users: userv1.OptionalNames{"b", "c"},
					},
				},
				"x": {
					Clusters: sets.New[string]("app.ci", "build01", "build02", "hosted-mgmt"),
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
			actual, actualErr := makeGroups(tc.openshiftPrivAdmins, tc.skipOpenshiftPrivAdmins, tc.peribolosConfig,
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
		name             string
		clients          map[string]ctrlruntimeclient.Client
		groups           map[string]GroupClusters
		dryRun           bool
		disabledClusters sets.Set[string]
		expected         error
		verifyFunc       func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error
	}{
		{
			name: "basic case",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewClientBuilder().WithRuntimeObjects(g01.DeepCopy()).Build(),
				"b02": fakeclient.NewClientBuilder().WithRuntimeObjects(g03.DeepCopy()).Build(),
			},
			groups: map[string]GroupClusters{
				"gh01-group": {
					Clusters: sets.New[string]("b01", "b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh01-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"gh01", "k01"},
					},
				},
				"gh02-group": {
					Clusters: sets.New[string]("b01", "b02"),
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
				"b01": fakeclient.NewClientBuilder().WithRuntimeObjects(g01.DeepCopy()).Build(),
				"b02": fakeclient.NewClientBuilder().WithRuntimeObjects(g03.DeepCopy()).Build(),
			},
			groups: map[string]GroupClusters{
				"gh01-group": {
					Clusters: sets.New[string]("b01", "b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh01-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"gh01", "k01"},
					},
				},
				"gh02-group": {
					Clusters: sets.New[string]("b01", "b02"),
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
			name: "basic case: b01 is disabled",
			clients: map[string]ctrlruntimeclient.Client{
				"b02": fakeclient.NewClientBuilder().WithRuntimeObjects(g03.DeepCopy()).Build(),
			},
			groups: map[string]GroupClusters{
				"gh01-group": {
					Clusters: sets.New[string]("b01", "b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh01-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"gh01", "k01"},
					},
				},
				"gh02-group": {
					Clusters: sets.New[string]("b01", "b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh02-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"gh02", "k02"},
					},
				},
			},
			disabledClusters: sets.New[string]("b01"),
			verifyFunc: func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error {
				client := clients["b02"]
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
						return fmt.Errorf("%s: actual does not match expected, diff: %s", "b02", diff)
					}
					if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "gh03-group"}, &userv1.Group{}); !errors.IsNotFound(err) {
						return fmt.Errorf("gh03-group is not deleted")
					}
				}
				return nil
			},
		},
		{
			name: "invalid group: duplicate members",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewClientBuilder().Build(),
				"b02": fakeclient.NewClientBuilder().Build(),
			},
			groups: map[string]GroupClusters{
				"gh01-group": {
					Clusters: sets.New("b01", "b02"),
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
		{
			name: "cluster client not available",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewClientBuilder().Build(),
			},
			groups: map[string]GroupClusters{
				"gh01-group": {
					Clusters: sets.New("b02"),
					Group: &userv1.Group{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "gh01-group",
							Labels: map[string]string{api.DPTPRequesterLabel: toolName},
						},
						Users: userv1.OptionalNames{"a"},
					},
				},
			},
			dryRun:   true,
			expected: kerrors.NewAggregate([]error{fmt.Errorf(`client for cluster "b02" is unavailable`)}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			actual := ensureGroups(ctx, tc.clients, tc.groups, 60, tc.dryRun, tc.disabledClusters)
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

func TestGetUsersWithoutKerberosID(t *testing.T) {

	u01 := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "a",
		},
		Identities: []string{"a"},
	}
	u02 := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "b",
		},
		Identities: []string{"b"},
	}
	u03 := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "c",
		},
		Identities: []string{"c", "d"},
	}

	testCases := []struct {
		name        string
		client      ctrlruntimeclient.Client
		kerberosIDs sets.Set[string]

		ExpectedUsersWithoutKerberosID map[string][]string
		expectedErr                    error
	}{
		{
			name: "basic case",
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(
				u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy()).Build(),
			kerberosIDs:                    sets.New[string]("a", "b"),
			ExpectedUsersWithoutKerberosID: map[string][]string{"c": {"c", "d"}},
			expectedErr:                    nil,
		},
		{
			name: "nothing to delete",
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(
				u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy()).Build(),
			kerberosIDs:                    sets.New[string]("a", "b", "c"),
			ExpectedUsersWithoutKerberosID: map[string][]string{},
			expectedErr:                    nil,
		},
		{
			name: "delete everyone",
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(
				u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy()).Build(),
			kerberosIDs:                    sets.New[string]("d", "e", "f"),
			ExpectedUsersWithoutKerberosID: map[string][]string{"a": {"a"}, "b": {"b"}, "c": {"c", "d"}},
			expectedErr:                    nil,
		},
		{
			name:                           "nothing from client",
			client:                         fakeclient.NewClientBuilder().Build(),
			kerberosIDs:                    sets.New[string]("d", "e", "f"),
			ExpectedUsersWithoutKerberosID: map[string][]string{},
			expectedErr:                    nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			actualUsers, err := getUsersWithoutKerberosID(ctx, tc.client, "b01", tc.kerberosIDs)
			if diff := cmp.Diff(tc.ExpectedUsersWithoutKerberosID, actualUsers, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func TestDeleteInvalidUsers(t *testing.T) {
	u01 := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "a",
		},
		Identities: []string{"a"},
	}
	i01 := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{
			Name: "a",
		},
		ProviderName:     "redhat.com",
		ProviderUserName: "a",
	}
	u02 := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "b",
		},
		Identities: []string{"b"},
	}
	i02 := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{
			Name: "b",
		},
		ProviderName:     "redhat.com",
		ProviderUserName: "b",
	}
	u03 := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "c",
		},
		Identities: []string{"c", "d"},
	}
	i03 := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{
			Name: "c",
		},
		ProviderName:     "redhat.com",
		ProviderUserName: "c",
	}
	i04 := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{
			Name: "d",
		},
		ProviderName:     "redhat.com",
		ProviderUserName: "d",
	}
	testCases := []struct {
		name        string
		clients     map[string]ctrlruntimeclient.Client
		kerberosIDs sets.Set[string]
		ciAdmins    sets.Set[string]
		verifyFunc  func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error
	}{
		{
			name: "basic case",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewClientBuilder().WithRuntimeObjects(
					u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy(),
					i01.DeepCopy(), i02.DeepCopy(), i03.DeepCopy(), i04.DeepCopy()).Build(),
				"b02": fakeclient.NewClientBuilder().WithRuntimeObjects(
					u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy(),
					i01.DeepCopy(), i02.DeepCopy(), i03.DeepCopy(), i04.DeepCopy()).Build(),
			},
			kerberosIDs: sets.New[string]("a", "b"),
			ciAdmins:    sets.New[string](),
			verifyFunc: func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error {
				for _, client := range clients {
					assert.True(t, isUser(ctx, client, "a"))
					assert.True(t, isIdentity(ctx, client, "a"))
					assert.True(t, isUser(ctx, client, "b"))
					assert.True(t, isIdentity(ctx, client, "b"))
					assert.False(t, isUser(ctx, client, "c"))
					assert.False(t, isIdentity(ctx, client, "c"))
					assert.False(t, isIdentity(ctx, client, "d"))
				}
				return nil
			},
		},
		{
			name: "nothing to delete",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewClientBuilder().WithRuntimeObjects(
					u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy(),
					i01.DeepCopy(), i02.DeepCopy(), i03.DeepCopy(), i04.DeepCopy()).Build(),
				"b02": fakeclient.NewClientBuilder().WithRuntimeObjects(
					u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy(),
					i01.DeepCopy(), i02.DeepCopy(), i03.DeepCopy(), i04.DeepCopy()).Build(),
			},
			kerberosIDs: sets.New[string]("a", "b", "c"),
			ciAdmins:    sets.New[string](),
			verifyFunc: func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error {
				for _, client := range clients {
					assert.True(t, isUser(ctx, client, "a"))
					assert.True(t, isIdentity(ctx, client, "a"))
					assert.True(t, isUser(ctx, client, "b"))
					assert.True(t, isIdentity(ctx, client, "b"))
					assert.True(t, isUser(ctx, client, "c"))
					assert.True(t, isIdentity(ctx, client, "c"))
					assert.True(t, isIdentity(ctx, client, "d"))
				}
				return nil
			},
		},
		{
			name: "delete everyone",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewClientBuilder().WithRuntimeObjects(u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy()).Build(),
				"b02": fakeclient.NewClientBuilder().WithRuntimeObjects(u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy()).Build(),
			},
			kerberosIDs: sets.New[string]("d", "e", "f"),
			ciAdmins:    sets.New[string](),
			verifyFunc: func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error {
				for _, client := range clients {
					assert.False(t, isUser(ctx, client, "a"))
					assert.False(t, isIdentity(ctx, client, "a"))
					assert.False(t, isUser(ctx, client, "b"))
					assert.False(t, isIdentity(ctx, client, "b"))
					assert.False(t, isUser(ctx, client, "c"))
					assert.False(t, isIdentity(ctx, client, "c"))
					assert.False(t, isIdentity(ctx, client, "d"))
				}
				return nil
			},
		},
		{
			name: "different users on each clusters",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewClientBuilder().WithRuntimeObjects(
					u01.DeepCopy(), u02.DeepCopy(),
					i01.DeepCopy(), i02.DeepCopy()).Build(),
				"b02": fakeclient.NewClientBuilder().WithRuntimeObjects(
					u03.DeepCopy(), i03.DeepCopy(), i04.DeepCopy()).Build(),
			},
			kerberosIDs: sets.New[string]("b", "c"),
			ciAdmins:    sets.New[string](),
			verifyFunc: func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error {
				assert.False(t, isUser(ctx, clients["b01"], "a"))
				assert.False(t, isIdentity(ctx, clients["b01"], "a"))
				assert.True(t, isUser(ctx, clients["b01"], "b"))
				assert.True(t, isIdentity(ctx, clients["b01"], "b"))
				assert.True(t, isUser(ctx, clients["b02"], "c"))
				assert.True(t, isIdentity(ctx, clients["b02"], "c"))
				assert.True(t, isIdentity(ctx, clients["b02"], "d"))
				return nil
			},
		},
		{
			name: "attempt to delete ci-admin",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewClientBuilder().WithRuntimeObjects(
					u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy(),
					i01.DeepCopy(), i02.DeepCopy(), i03.DeepCopy(), i04.DeepCopy()).Build(),
				"b02": fakeclient.NewClientBuilder().WithRuntimeObjects(
					u01.DeepCopy(), u02.DeepCopy(), u03.DeepCopy(),
					i01.DeepCopy(), i02.DeepCopy(), i03.DeepCopy(), i04.DeepCopy()).Build(),
			},
			kerberosIDs: sets.New[string]("b"),
			ciAdmins:    sets.New[string]("a"),
			verifyFunc: func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error {
				for _, client := range clients {
					assert.True(t, isUser(ctx, client, "a"))
					assert.True(t, isIdentity(ctx, client, "a"))
					assert.True(t, isUser(ctx, client, "b"))
					assert.True(t, isIdentity(ctx, client, "b"))
					assert.False(t, isUser(ctx, client, "c"))
					assert.False(t, isIdentity(ctx, client, "c"))
					assert.False(t, isIdentity(ctx, client, "d"))
				}
				return nil
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			err := deleteInvalidUsers(ctx, tc.clients, tc.kerberosIDs, tc.ciAdmins, false)
			if err != nil {
				t.Errorf("%s: unexpected error occurred: %v", tc.name, err)
			}
			if err == nil && tc.verifyFunc != nil {
				if err := tc.verifyFunc(ctx, tc.clients); err != nil {
					t.Errorf("%s: unexpected error occurred: %v", tc.name, err)
				}
			}
		})
	}
}

func isUser(ctx context.Context, client ctrlruntimeclient.Client, user string) bool {
	err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: user}, &userv1.User{})
	return err == nil
}

func isIdentity(ctx context.Context, client ctrlruntimeclient.Client, user string) bool {
	err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: user}, &userv1.Identity{})
	return err == nil
}
