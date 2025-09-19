package multi_stage

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	csiapi "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
	testhelper_kube "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
)

func TestParseNamespaceUID(t *testing.T) {
	for _, tc := range []struct {
		name, uidRange, err string
		uid                 int64
	}{{
		name:     "valid",
		uidRange: "1007160000/10000",
		uid:      1007160000,
	}, {
		name: "empty",
		err:  "invalid namespace UID range: ",
	}, {
		name:     "invalid format",
		uidRange: "invalid format",
		err:      "invalid namespace UID range: invalid format",
	}, {
		name:     "missing UID",
		uidRange: "/10000",
		err:      "invalid namespace UID range: /10000",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			uid, err := parseNamespaceUID(tc.uidRange)
			var errStr string
			if err != nil {
				errStr = err.Error()
			}
			testhelper.Diff(t, "uid", uid, tc.uid)
			testhelper.Diff(t, "error", errStr, tc.err, testhelper.EquateErrorMessage)
		})
	}
}

func TestCreateSPCs(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = csiapi.AddToScheme(scheme)

	credential1 := api.CredentialReference{Name: "credential1", Collection: "test", MountPath: "/tmp/path1"}
	credential2 := api.CredentialReference{Name: "credential2", Collection: "test-2", MountPath: "/tmp/path2"}

	newGroupedSPC := func(collection, mountPath, ns string, credentials []api.CredentialReference) csiapi.SecretProviderClass {
		secret, _ := buildGCPSecretsParameter(credentials)
		spc := buildSecretProviderClass(getSPCName(ns, collection, mountPath, credentials), ns, secret)
		// Set ResourceVersion for fake client compatibility
		spc.ResourceVersion = "1"
		return *spc
	}

	newCensoringSPC := func(collection, credName, ns string) csiapi.SecretProviderClass {
		credential := api.CredentialReference{Name: credName, Collection: collection}
		credentials := []api.CredentialReference{credential}
		secret, _ := buildGCPSecretsParameter(credentials)
		censorMountPath := fmt.Sprintf("/censor/%s", credName)

		spc := buildSecretProviderClass(getSPCName(ns, collection, censorMountPath, credentials), ns, secret)
		// Set ResourceVersion for fake client compatibility
		spc.ResourceVersion = "1"
		return *spc
	}

	for _, tc := range []struct {
		name         string
		pre          []api.LiteralTestStep
		test         []api.LiteralTestStep
		post         []api.LiteralTestStep
		expectedSPCs csiapi.SecretProviderClassList
	}{
		{
			name: "no credentials",
		},
		{
			name: "single credential",
			pre:  []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential1}}},
			expectedSPCs: csiapi.SecretProviderClassList{
				Items: []csiapi.SecretProviderClass{
					// Grouped SPC for the credential at its mount path
					newGroupedSPC(credential1.Collection, credential1.MountPath, "test-ns", []api.CredentialReference{credential1}),
					// Individual SPC for censoring
					newCensoringSPC(credential1.Collection, credential1.Name, "test-ns"),
				},
			},
		},
		{
			name: "multiple credentials different paths",
			pre:  []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential1}}},
			test: []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential2}}},
			expectedSPCs: csiapi.SecretProviderClassList{
				Items: []csiapi.SecretProviderClass{
					// Grouped SPCs
					newGroupedSPC(credential1.Collection, credential1.MountPath, "test-ns", []api.CredentialReference{credential1}),
					newGroupedSPC(credential2.Collection, credential2.MountPath, "test-ns", []api.CredentialReference{credential2}),
					// Individual censoring SPCs
					newCensoringSPC(credential1.Collection, credential1.Name, "test-ns"),
					newCensoringSPC(credential2.Collection, credential2.Name, "test-ns"),
				},
			},
		},
		{
			name: "credentials with same collection and path grouped",
			pre:  []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential1, {Name: "credential3", Collection: "test", MountPath: "/tmp/path1"}}}},
			expectedSPCs: csiapi.SecretProviderClassList{
				Items: []csiapi.SecretProviderClass{
					// Grouped SPC with both credentials at same path
					newGroupedSPC("test", "/tmp/path1", "test-ns", []api.CredentialReference{
						credential1,
						{Name: "credential3", Collection: "test", MountPath: "/tmp/path1"},
					}),
					// Individual censoring SPCs
					newCensoringSPC(credential1.Collection, credential1.Name, "test-ns"),
					newCensoringSPC("test", "credential3", "test-ns"),
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			crclient := &testhelper_kube.FakePodExecutor{
				LoggingClient: loggingclient.New(
					fakectrlruntimeclient.NewClientBuilder().
						WithScheme(scheme).
						Build(), nil),
			}
			fakeClient := &testhelper_kube.FakePodClient{
				FakePodExecutor: crclient,
			}
			step := &multiStageTestStep{
				pre:     tc.pre,
				test:    tc.test,
				post:    tc.post,
				jobSpec: &api.JobSpec{},
				client:  fakeClient,
			}
			step.jobSpec.SetNamespace("test-ns")
			err := step.createSPCs(context.TODO())
			if err != nil {
				t.Fatal(err)
			}

			spcs := &csiapi.SecretProviderClassList{}
			if err := crclient.List(context.TODO(), spcs, ctrlruntimeclient.InNamespace(step.jobSpec.Namespace())); err != nil {
				t.Fatal(err)
			}

			// Check we have the expected number of SPCs
			if len(spcs.Items) != len(tc.expectedSPCs.Items) {
				t.Fatalf("expected %d SPCs but got %d", len(tc.expectedSPCs.Items), len(spcs.Items))
			}

			// Create maps for easier comparison (since order might vary)
			actualSPCs := make(map[string]csiapi.SecretProviderClass)
			for _, spc := range spcs.Items {
				actualSPCs[spc.Name] = spc
			}

			expectedSPCs := make(map[string]csiapi.SecretProviderClass)
			for _, spc := range tc.expectedSPCs.Items {
				expectedSPCs[spc.Name] = spc
			}

			// Check that all expected SPCs exist
			for name, expectedSPC := range expectedSPCs {
				actualSPC, exists := actualSPCs[name]
				if !exists {
					t.Fatalf("expected SPC %s not found", name)
				}

				// Compare the important fields
				if actualSPC.Spec.Provider != expectedSPC.Spec.Provider {
					t.Errorf("SPC %s: expected provider %s but got %s", name, expectedSPC.Spec.Provider, actualSPC.Spec.Provider)
				}
				if actualSPC.Spec.Parameters["auth"] != expectedSPC.Spec.Parameters["auth"] {
					t.Errorf("SPC %s: expected auth %s but got %s", name, expectedSPC.Spec.Parameters["auth"], actualSPC.Spec.Parameters["auth"])
				}
				// Note: We don't compare secrets parameter exactly since it's complex YAML
			}
		})
	}
}

func TestSetupRBAC(t *testing.T) {
	jobSpec := &api.JobSpec{}
	jobSpec.SetNamespace("ci-op-xxxx")

	for _, tc := range []struct {
		name string
		step *multiStageTestStep

		wantSAs          []corev1.ServiceAccount
		wantRoles        []rbacv1.Role
		wantRoleBindings []rbacv1.RoleBinding
	}{
		{
			name: "Create role binding for nested podman",
			step: &multiStageTestStep{
				name:    "nested-podman",
				jobSpec: jobSpec,
				config: &api.ReleaseBuildConfiguration{
					Tests: []api.TestStepConfiguration{{
						As: "test",
						MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
							Test: []api.LiteralTestStep{{
								As:           "test",
								NestedPodman: true,
							}},
						},
					}},
				},
				test: []api.LiteralTestStep{{
					As:           "test",
					NestedPodman: true,
				}},
				requireNestedPodman: true,
			},
			wantSAs: []corev1.ServiceAccount{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "nested-podman",
					Namespace:       "ci-op-xxxx",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/multi-stage-test": "nested-podman"},
				},
				ImagePullSecrets: []corev1.LocalObjectReference{{Name: "random-pull-secret"}},
			}},
			wantRoles: []rbacv1.Role{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "nested-podman",
						Namespace:       "ci-op-xxxx",
						ResourceVersion: "1",
						Labels:          map[string]string{"ci.openshift.io/multi-stage-test": "nested-podman"},
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create", "list"},
							APIGroups: []string{"rbac.authorization.k8s.io"},
							Resources: []string{"rolebindings", "roles"},
						},
						{
							Verbs:         []string{"get", "update"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"nested-podman", "test-done-signal"},
						},
						{
							Verbs:     []string{"get"},
							APIGroups: []string{"", "image.openshift.io"},
							Resources: []string{"imagestreams/layers"},
						},
					},
				},
			},
			wantRoleBindings: []rbacv1.RoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "nested-podman",
						Namespace:       "ci-op-xxxx",
						ResourceVersion: "1",
						Labels:          map[string]string{"ci.openshift.io/multi-stage-test": "nested-podman"},
					},
					Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "nested-podman"}},
					RoleRef:  rbacv1.RoleRef{Kind: "Role", Name: "nested-podman"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "nested-podman-view",
						Namespace:       "ci-op-xxxx",
						ResourceVersion: "1",
						Labels:          map[string]string{"ci.openshift.io/multi-stage-test": "nested-podman"},
					},
					Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "nested-podman"}},
					RoleRef:  rbacv1.RoleRef{Kind: "ClusterRole", Name: "view"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "nested-podman-nested-podman-scc-creater",
						Namespace:       "ci-op-xxxx",
						ResourceVersion: "1",
					},
					Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "nested-podman"}},
					RoleRef:  rbacv1.RoleRef{Kind: "ClusterRole", Name: "nested-podman-creater"},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.TODO()

			client := fakectrlruntimeclient.NewClientBuilder().
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
						if sa, ok := obj.(*corev1.ServiceAccount); ok {
							sa.ImagePullSecrets = []corev1.LocalObjectReference{{
								Name: "random-pull-secret",
							}}
						}
						return client.Create(ctx, obj, opts...)
					},
					Get: func(ctx context.Context, client ctrlruntimeclient.WithWatch, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
						if err := client.Get(ctx, key, obj, opts...); err != nil {
							return err
						}

						if ns, ok := obj.(*corev1.Namespace); ok && key.Name == jobSpec.Namespace() {
							if ns.Annotations == nil {
								ns.Annotations = make(map[string]string)
							}
							ns.Annotations["security.openshift.io/MinimallySufficientPodSecurityStandard"] = "privileged"
						}

						return nil
					},
				}).
				WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: jobSpec.Namespace()}}).
				Build()

			tc.step.client = &testhelper_kube.FakePodClient{
				FakePodExecutor: &testhelper_kube.FakePodExecutor{
					LoggingClient: loggingclient.New(client, nil),
				},
			}

			if err := tc.step.setupRBAC(ctx); err != nil {
				t.Errorf("unexpected error: %s", err)
			}

			sas := corev1.ServiceAccountList{}
			if err := client.List(ctx, &sas, &ctrlruntimeclient.ListOptions{}); err != nil {
				t.Fatal("Error listing SAs")
			}

			if diff := cmp.Diff(tc.wantSAs, sas.Items, cmpopts.SortSlices(func(a, b corev1.ServiceAccount) bool {
				return strings.Compare(a.Name, b.Name) == -1
			})); diff != "" {
				t.Errorf("SAs are different:\n%s", diff)
			}

			roles := rbacv1.RoleList{}
			if err := client.List(ctx, &roles, &ctrlruntimeclient.ListOptions{}); err != nil {
				t.Fatal("Error listing Roles")
			}

			if diff := cmp.Diff(tc.wantRoles, roles.Items, cmpopts.SortSlices(func(a, b rbacv1.Role) bool {
				return strings.Compare(a.Name, b.Name) == -1
			})); diff != "" {
				t.Errorf("Roles are different:\n%s", diff)
			}

			rolebindings := rbacv1.RoleBindingList{}
			if err := client.List(ctx, &rolebindings, &ctrlruntimeclient.ListOptions{}); err != nil {
				t.Fatal("Error listing RoleBindings")
			}

			if diff := cmp.Diff(tc.wantRoleBindings, rolebindings.Items, cmpopts.SortSlices(func(a, b rbacv1.RoleBinding) bool {
				return strings.Compare(a.Name, b.Name) == -1
			})); diff != "" {
				t.Errorf("RoleBindings are different:\n%s", diff)
			}
		})
	}
}
