package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	userv1 "github.com/openshift/api/user/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := userv1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestTagsToDelete(t *testing.T) {

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
	ciAdmins := &userv1.Group{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ci-admins",
		},
	}

	testCases := []struct {
		name       string
		clients    map[string]ctrlruntimeclient.Client
		mapping    map[string]string
		dryRun     bool
		expected   error
		verifyFunc func(ctx context.Context, clients map[string]ctrlruntimeclient.Client) error
	}{
		{
			name: "basic case",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewFakeClient(ciAdmins.DeepCopy(), g01.DeepCopy()),
				"b02": fakeclient.NewFakeClient(ciAdmins.DeepCopy(), g03.DeepCopy()),
			},
			mapping: map[string]string{"gh01": "k01", "gh02": "k02"},
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
						if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "ci-admins"}, &userv1.Group{}); err != nil {
							return fmt.Errorf("we cannot delete ci-admins group")
						}
						if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "gh03-group"}, &userv1.Group{}); !errors.IsNotFound(err) {
							return fmt.Errorf("we did not delete gh03-group group")
						}
					}
				}
				return nil
			},
		},
		{
			name: "basic case: dryRun=true",
			clients: map[string]ctrlruntimeclient.Client{
				"b01": fakeclient.NewFakeClient(ciAdmins.DeepCopy(), g01.DeepCopy()),
				"b02": fakeclient.NewFakeClient(ciAdmins.DeepCopy(), g03.DeepCopy()),
			},
			mapping: map[string]string{"gh01": "k01", "gh02": "k02"},
			dryRun:  true,
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			actual := ensureGroups(ctx, tc.clients, tc.mapping, tc.dryRun)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if tc.verifyFunc != nil {
				if err := tc.verifyFunc(ctx, tc.clients); err != nil {
					t.Errorf("%s: unexpected error occurred: %v", tc.name, err)
				}
			}
		})
	}
}
