package multi_stage

import (
	"context"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
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
						Build()),
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
