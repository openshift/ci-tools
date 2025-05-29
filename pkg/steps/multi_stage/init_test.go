package multi_stage

import (
	"context"
	"fmt"
	"testing"

	"github.com/GoogleCloudPlatform/secrets-store-csi-driver-provider-gcp/config"
	"github.com/google/go-cmp/cmp"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	csiapi "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	"sigs.k8s.io/yaml"

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

	credential1 := api.CredentialReference{Name: "credential1", Collection: "test"}
	credential2 := api.CredentialReference{Name: "credential2", Collection: "test-2"}

	newSPC := func(collection, name, ns string) csiapi.SecretProviderClass {
		secret, _ := getSecretString(collection, name)

		return csiapi.SecretProviderClass{
			TypeMeta: meta.TypeMeta{
				Kind:       "SecretProviderClass",
				APIVersion: csiapi.GroupVersion.String(),
			},
			ObjectMeta: meta.ObjectMeta{
				Name:            fmt.Sprintf("%s-%s-spc", ns, name),
				Namespace:       ns,
				ResourceVersion: "1",
			},
			Spec: csiapi.SecretProviderClassSpec{
				Provider: "gcp",
				Parameters: map[string]string{
					"auth":    "provider-adc",
					"secrets": secret,
				},
			},
		}
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
					newSPC(credential1.Collection, credential1.Name, "test-ns"),
				},
			},
		},
		{
			name: "multiple credentials",
			pre:  []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential1}}},
			test: []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential2}}},
			expectedSPCs: csiapi.SecretProviderClassList{
				Items: []csiapi.SecretProviderClass{
					newSPC(credential1.Collection, credential1.Name, "test-ns"),
					newSPC(credential2.Collection, credential2.Name, "test-ns"),
				},
			},
		},
		{
			name: "multiple credentials - duplicated",
			pre:  []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential1}}},
			test: []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential2}}},
			post: []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential1}}},
			expectedSPCs: csiapi.SecretProviderClassList{
				Items: []csiapi.SecretProviderClass{
					newSPC(credential1.Collection, credential1.Name, "test-ns"),
					newSPC(credential2.Collection, credential2.Name, "test-ns"),
				},
			},
		},
		{
			name: "multiple credentials - second set of duplicates",
			pre:  []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential1, credential2}}},
			test: []api.LiteralTestStep{{Credentials: []api.CredentialReference{credential2}}},
			expectedSPCs: csiapi.SecretProviderClassList{
				Items: []csiapi.SecretProviderClass{
					newSPC(credential1.Collection, credential1.Name, "test-ns"),
					newSPC(credential2.Collection, credential2.Name, "test-ns"),
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

			if diff := cmp.Diff(spcs.Items, tc.expectedSPCs.Items); diff != "" {
				t.Fatalf("unexpected secret provider classes (-want, +got) = %v", diff)
			}
		})
	}
}

func TestGetSecretString(t *testing.T) {
	name := "secret-name"
	collection := "collection1"

	yamlString, err := getSecretString(collection, name)
	if err != nil {
		t.Fatalf("unexpected error getting secret string: %v", err)
	}

	if yamlString == "" {
		t.Fatal("expected non-empty secret string")
	}

	var secrets []config.Secret
	err = yaml.Unmarshal([]byte(yamlString), &secrets)
	if err != nil {
		t.Fatalf("Failed to unmarshal YAML output: %v", err)
	}

	if len(secrets) != 1 {
		t.Fatalf("Expected exactly one secret but got %d", len(secrets))
	}

	expectedSecret := config.Secret{
		ResourceName: fmt.Sprintf("projects/%s/secrets/%s__%s/versions/latest", GSMproject, collection, name),
		FileName:     name,
	}

	// Compare the actual and expected secret
	if diff := cmp.Diff(expectedSecret, secrets[0]); diff != "" {
		t.Errorf("Secret struct mismatch (-want +got):\n%s", diff)
	}
}
