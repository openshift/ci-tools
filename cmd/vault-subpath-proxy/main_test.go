package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/vaultclient"
)

func TestProxy(t *testing.T) {
	logrus.SetLevel(logrus.TraceLevel)

	vaultAddr := testhelper.Vault(t)

	vaultClient, err := api.NewClient(&api.Config{Address: "http://" + vaultAddr})
	if err != nil {
		t.Fatalf("failed to construct vault client: %v", err)
	}
	vaultClient.SetToken(testhelper.VaultTestingRootToken)

	team1Policy := `
path "secret/data/team-1/*" {
  capabilities = ["create", "update", "read", "delete"]
}

path "secret/metadata/team-1/*" {
  capabilities = ["list"]
}`
	if err := vaultClient.Sys().PutPolicy("team-1", team1Policy); err != nil {
		t.Fatalf("failed to create team-1 policy: %v", err)
	}
	if err := vaultClient.Sys().TuneMount("secret", api.MountConfigInput{ListingVisibility: "unauth"}); err != nil {
		t.Fatalf("failed to set secret mount visibility: %v", err)
	}

	if err := writeKV(vaultClient, "/v1/secret/data/top-level", map[string]string{"foo": "bar"}); err != nil {
		t.Fatalf("failed to write top-level secret: %v", err)
	}
	if err := writeKV(vaultClient, "/v1/secret/data/team-1/team-1", map[string]string{"foo": "bar"}); err != nil {
		t.Fatalf("failed to write top-level secret: %v", err)
	}
	if err := writeKV(vaultClient, "/v1/secret/data/team-2/team-2", map[string]string{"foo": "bar"}); err != nil {
		t.Fatalf("failed to write top-level secret: %v", err)
	}
	team1TokenResponse, err := vaultClient.Auth().Token().Create(&api.TokenCreateRequest{Policies: []string{"team-1"}})
	if err != nil {
		t.Errorf("failed to create token with team-1 policy: %v", err)
	}
	rootDirect, err := vaultclient.New("http://"+vaultAddr, testhelper.VaultTestingRootToken)
	if err != nil {
		t.Fatalf("failed to construct rootDirect client: %v", err)
	}

	proxyServerPort := testhelper.GetFreePort(t)
	proxyServer, err := createProxyServer("http://"+vaultAddr, "127.0.0.1:"+proxyServerPort, "secret", nil, rootDirect)
	if err != nil {
		t.Fatalf("failed to create proxy server: %v", err)
	}

	go func() {
		if err := proxyServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("proxy server failed to listen: %v", err)
		}
	}()
	t.Cleanup(func() {
		if err := proxyServer.Close(); err != nil {
			t.Errorf("failed to close proxy: %v", err)
		}
	})
	testhelper.WaitForHTTP200("http://127.0.0.1:"+proxyServerPort+"/v1/sys/health", "vault-subpath-proxy", 90, t)

	rootProxy, err := vaultclient.New("http://127.0.0.1:"+proxyServerPort, testhelper.VaultTestingRootToken)
	if err != nil {
		t.Fatalf("failed to construct rootProxy client: %v", err)
	}
	team1Direct, err := vaultclient.New("http://"+vaultAddr, team1TokenResponse.Auth.ClientToken)
	if err != nil {
		t.Fatalf("failed to construct team1Direct client: %v", err)
	}
	team1Proxy, err := vaultclient.New("http://127.0.0.1:"+proxyServerPort, team1TokenResponse.Auth.ClientToken)
	if err != nil {
		t.Fatalf("failed to construct team1Proxy client: %v", err)
	}

	type clientTestCase struct {
		clientName         string
		client             *vaultclient.VaultClient
		expectedStatusCode int
		expectedKeys       []interface{}
	}

	testCases := []struct {
		path            string
		clientTestCases []clientTestCase
	}{
		{
			path: "secret/metadata",
			clientTestCases: []clientTestCase{
				{"rootDirect", rootDirect, 200, []interface{}{"team-1/", "team-2/", "top-level"}},
				{"team1Direct", team1Direct, 403, nil},
				{"rootProxy", rootProxy, 200, []interface{}{"team-1/", "team-2/", "top-level"}},
				{"team1Proxied", team1Proxy, 200, []interface{}{"team-1/"}},
			},
		},
		{
			path: "secret/metadata/team-1",
			clientTestCases: []clientTestCase{
				{"rootDirect", rootDirect, 200, []interface{}{"team-1"}},
				{"team1Direct", team1Direct, 200, []interface{}{"team-1"}},
				{"rootProxy", rootProxy, 200, []interface{}{"team-1"}},
				{"team1Proxied", team1Proxy, 200, []interface{}{"team-1"}},
			},
		},
		{
			path: "secret/metadata/team-2",
			clientTestCases: []clientTestCase{
				{"rootDirect", rootDirect, 200, []interface{}{"team-2"}},
				{"team1Direct", team1Direct, 403, nil},
				{"rootProxy", rootProxy, 200, []interface{}{"team-2"}},
				{"team1Proxied", team1Proxy, 403, nil},
			},
		},
		{
			path: "secret/metadata/undefined",
			clientTestCases: []clientTestCase{
				{"rootDirect", rootDirect, 404, nil},
				{"team1Direct", team1Direct, 403, nil},
				{"rootProxy", rootProxy, 404, nil},
				{"team1Proxied", team1Proxy, 403, nil},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			for _, clientTestCase := range tc.clientTestCases {
				clientTestCase := clientTestCase
				t.Run(fmt.Sprintf("%s: %d - %v", clientTestCase.clientName, clientTestCase.expectedStatusCode, clientTestCase.expectedKeys), func(t *testing.T) {
					t.Parallel()
					result, err := clientTestCase.client.Logical().List(tc.path)
					if err != nil {
						responseErr, ok := err.(*api.ResponseError)
						if !ok {
							t.Fatalf("got an error that was not an *api.ResponseError: type: %T, value: %v", err, err)
						}
						if responseErr.StatusCode != clientTestCase.expectedStatusCode {
							t.Fatalf("expected status code %d, got status code %d", clientTestCase.expectedStatusCode, responseErr.StatusCode)
						}
						return
					}

					var actualKeys interface{}
					if result != nil && result.Data != nil && result.Data["keys"] != nil {
						actualKeys = result.Data["keys"]
					}
					var expected interface{}
					if len(clientTestCase.expectedKeys) > 0 {
						expected = clientTestCase.expectedKeys
					}
					if diff := cmp.Diff(expected, actualKeys); diff != "" {
						t.Errorf("expectedKeys differs from actual keys: %s", diff)
					}
				})
			}
		})
	}

	kvKeyValidationTestCases := []struct {
		name               string
		data               map[string]string
		expectedStatusCode int
		expectedErrors     []string

		clusters        map[string]ctrlruntimeclient.Client
		expectedSecrets map[string]*corev1.SecretList
	}{
		{
			name:               "Slash in key name is refused",
			data:               map[string]string{"invalid/key": "data"},
			expectedStatusCode: 400,
			expectedErrors:     []string{`key invalid/key is invalid: must match regex ^[a-zA-Z0-9\.\-_]+$`},
		},
		{
			name: "Name with dots is allowed",
			data: map[string]string{"sa.ci-chat-bot.api.ci.config": "data"},
		},
		{
			name: "All letters key is allowed",
			data: map[string]string{"key": "data"},
		},
		{
			name: "Multiple namespaces are allowed",
			data: map[string]string{"secretsync/target-namespace": "one-namespace,another-namespace"},
		},
		{
			name:               "Invalid value in one of multiple namespaces fails validation",
			data:               map[string]string{"secretsync/target-namespace": "one-namespace,invalid/namespace"},
			expectedStatusCode: 400,
			expectedErrors:     []string{"value of key secretsync/target-namespace is invalid: [a lowercase RFC 1123 label must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character (e.g. 'my-name',  or '123-abc', regex used for validation is '[a-z0-9]([-a-z0-9]*[a-z0-9])?')]"},
		},
		{
			name:               "Namespace key with invalid value is refused",
			data:               map[string]string{"secretsync/target-namespace": "invalid/value"},
			expectedStatusCode: 400,
			expectedErrors:     []string{"value of key secretsync/target-namespace is invalid: [a lowercase RFC 1123 label must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character (e.g. 'my-name',  or '123-abc', regex used for validation is '[a-z0-9]([-a-z0-9]*[a-z0-9])?')]"},
		},
		{
			name: "Namespace key with valid value is allowed",
			data: map[string]string{"secretsync/target-namespace": "some-namespace"},
		},
		{
			name:               "Name key with invalid value is refused",
			data:               map[string]string{"secretsync/target-name": "invalid/value"},
			expectedStatusCode: 400,
			expectedErrors:     []string{"value of key secretsync/target-name is invalid: [a lowercase RFC 1123 label must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character (e.g. 'my-name',  or '123-abc', regex used for validation is '[a-z0-9]([-a-z0-9]*[a-z0-9])?')]"},
		},
		{
			name: "Name key with valid value is allowed",
			data: map[string]string{"secretsync/target-name": "some-name"},
		},
		{
			name: "Target cluster key is allowed",
			data: map[string]string{"secretsync/target-clusters": "whatever"},
		},
		{
			name: "Secret gets synced in multiple namespaces",
			data: map[string]string{
				"secretsync/target-namespace": "target-namespace-1,target-namespace-2",
				"secretsync/target-name":      "secret",
				"some-other-secret":           "some-value",
			},
			clusters: map[string]ctrlruntimeclient.Client{
				"a": fakectrlruntimeclient.NewFakeClient(),
			},
			expectedSecrets: map[string]*corev1.SecretList{
				"a": {Items: []corev1.Secret{
					{ObjectMeta: metav1.ObjectMeta{Namespace: "target-namespace-1", Name: "secret"}, Data: map[string][]byte{"some-other-secret": []byte("some-value")}},
					{ObjectMeta: metav1.ObjectMeta{Namespace: "target-namespace-2", Name: "secret"}, Data: map[string][]byte{"some-other-secret": []byte("some-value")}},
				}},
			},
		},
		{
			name: "Secret gets synced into multiple clusters, create in one, update in the other",
			data: map[string]string{
				"secretsync/target-namespace": "default",
				"secretsync/target-name":      "secret",
				"some-other-secret":           "some-value",
			},
			clusters: map[string]ctrlruntimeclient.Client{
				"a": fakectrlruntimeclient.NewFakeClient(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secret"}}),
				"b": fakectrlruntimeclient.NewFakeClient(),
			},
			expectedSecrets: map[string]*corev1.SecretList{
				"a": {Items: []corev1.Secret{
					{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secret"}, Data: map[string][]byte{"some-other-secret": []byte("some-value")}}},
				},
				"b": {Items: []corev1.Secret{
					{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secret"}, Data: map[string][]byte{"some-other-secret": []byte("some-value")}}},
				},
			},
		},
		{
			name: "Secret gets synced into multiple clusters, secret targets only one and gets only synced into that one",
			data: map[string]string{
				"secretsync/target-namespace": "default",
				"secretsync/target-name":      "secret",
				"secretsync/target-clusters":  "a",
				"some-secret":                 "some-value",
			},
			clusters: map[string]ctrlruntimeclient.Client{
				"a": fakectrlruntimeclient.NewFakeClient(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secret"}}),
				"b": fakectrlruntimeclient.NewFakeClient(),
			},
			expectedSecrets: map[string]*corev1.SecretList{
				"a": {Items: []corev1.Secret{
					{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secret"}, Data: map[string][]byte{"some-secret": []byte("some-value")}}},
				},
			},
		},
		{
			name: "Secret sync retains pre-existing keys",
			data: map[string]string{
				"secretsync/target-namespace": "default",
				"secretsync/target-name":      "secret",
				"secretsync/target-clusters":  "a",
				"some-third-secret":           "some-value",
			},
			clusters: map[string]ctrlruntimeclient.Client{
				"a": fakectrlruntimeclient.NewFakeClient(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secret"}, Data: map[string][]byte{"pre-existing": []byte("value")}}),
			},
			expectedSecrets: map[string]*corev1.SecretList{
				"a": {Items: []corev1.Secret{
					{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secret"}, Data: map[string][]byte{
						"some-third-secret": []byte("some-value"),
						"pre-existing":      []byte("value"),
					}}},
				},
			},
		},
	}

	kvUpdateTransport := proxyServer.Handler.(*httputil.ReverseProxy).Transport.(*kvUpdateTransport)
	kvUpdateTransport.synchronousSecretSync = true
	for i, tc := range kvKeyValidationTestCases {
		t.Run(tc.name, func(t *testing.T) {
			kvUpdateTransport.kubeClients = func() map[string]ctrlruntimeclient.Client { return tc.clusters }

			var actualStatusCode int
			var actualErrors []string
			err := rootProxy.UpsertKV("secret/kv-key-validation-tests/"+strconv.Itoa(i), tc.data)
			if err != nil {
				responseErr, ok := err.(*api.ResponseError)
				if !ok {
					t.Fatalf("got an error back that was not a response error but a %T", err)
				}
				actualStatusCode = responseErr.StatusCode
				actualErrors = responseErr.Errors
			}
			if actualStatusCode != tc.expectedStatusCode {
				t.Errorf("expected status code %d, got %d", tc.expectedStatusCode, actualStatusCode)
			}
			if diff := cmp.Diff(actualErrors, tc.expectedErrors); diff != "" {
				t.Errorf("actual errors differ from expected: %s", diff)
			}

			actualSecrets := map[string]*corev1.SecretList{}
			for clusterName, client := range kvUpdateTransport.kubeClients() {
				secrets := &corev1.SecretList{}
				if err := client.List(context.Background(), secrets); err != nil {
					t.Errorf("failed to list secrets for cluster %s: %v", clusterName, err)
				}
				if len(secrets.Items) > 0 {
					actualSecrets[clusterName] = secrets
				}
			}
			if diff := cmp.Diff(tc.expectedSecrets, actualSecrets, testhelper.RuntimeObjectIgnoreRvTypeMeta, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("expected secrets differ from actual: %s", diff)
			}
		})
	}

	t.Run("keyConflictTestCases", func(t *testing.T) {
		keyConflictTestCases := []struct {
			name               string
			targetSecretName   string
			isDelete           bool
			data               map[string]string
			expectedStatusCode int
			expectedErrors     []string
		}{
			{
				name:             "Initial secret gets created",
				targetSecretName: "some-secret",
				data: map[string]string{
					"secretsync/target-namespace": "default",
					"secretsync/target-name":      "secret",
					"some-secret-key":             "some-value",
				},
			},
			{
				name:             "Creating another secret with the same target and same key fails",
				targetSecretName: "second-secret",
				data: map[string]string{
					"secretsync/target-namespace": "default",
					"secretsync/target-name":      "secret",
					"some-secret-key":             "some-value",
				},
				expectedStatusCode: 400,
				expectedErrors:     []string{"key some-secret-key in secret default/secret is already claimed"},
			},
			{
				name:             "Creating another secret for multiple namespaces that include the same target and same key fails",
				targetSecretName: "second-secret",
				data: map[string]string{
					"secretsync/target-namespace": "default,other",
					"secretsync/target-name":      "secret",
					"some-secret-key":             "some-value",
				},
				expectedStatusCode: 400,
				expectedErrors:     []string{"key some-secret-key in secret default/secret is already claimed"},
			},
			{
				name:             "Creating another secret with the same target but different keys succeeds",
				targetSecretName: "third-secret",
				data: map[string]string{
					"secretsync/target-namespace": "default",
					"secretsync/target-name":      "secret",
					"another-secret-key":          "some-value",
				},
			},
			{
				name:             "Updating the initial secret to replace the key succeeds",
				targetSecretName: "some-secret",
				data: map[string]string{
					"secretsync/target-namespace": "default",
					"secretsync/target-name":      "secret",
					"third-secret-key":            "some-value",
				},
			},
			{
				name:             "Creating another secret that targets what the initial secret used to reference succeeds now",
				targetSecretName: "second-secret",
				data: map[string]string{
					"secretsync/target-namespace": "default",
					"secretsync/target-name":      "secret",
					"some-secret-key":             "some-value",
				},
			},
			{
				name:             "Creating a fourth secret with the same target and key as the previous one fails",
				targetSecretName: "fourth-secret",
				data: map[string]string{
					"secretsync/target-namespace": "default",
					"secretsync/target-name":      "secret",
					"some-secret-key":             "some-value",
				},
				expectedStatusCode: 400,
				expectedErrors:     []string{"key some-secret-key in secret default/secret is already claimed"},
			},
			{
				name:             "Deleting the second secret succeeds",
				targetSecretName: "second-secret",
				isDelete:         true,
			},
			{
				name:             "Creating a fourth secret with the same target and key as the deleted secret succeeds",
				targetSecretName: "fourth-secret",
				data: map[string]string{
					"secretsync/target-namespace": "default",
					"secretsync/target-name":      "secret",
					"some-secret-key":             "some-value",
				},
			},
			{
				name:             "Updating the previous secret and adding another key succeeds",
				targetSecretName: "fourth-secret",
				data: map[string]string{
					"secretsync/target-namespace": "default",
					"secretsync/target-name":      "secret",
					"some-secret-key":             "some-value",
					"some-secret-key-2":           "another-value",
				},
			},
			{
				name:             "Creating a secret with no target",
				targetSecretName: "selfmanaged-secret",
				data: map[string]string{
					"selfmanaged": "some-value",
				},
			},
			{
				name:             "Creating another secret with no target and the same key succeeds",
				targetSecretName: "selfmanaged-secret-2",
				data: map[string]string{
					"selfmanaged": "some-value",
				},
			},
		}
		for _, tc := range keyConflictTestCases {
			t.Run(tc.name, func(t *testing.T) {
				var actualStatusCode int
				var actualErrors []string

				var err error
				if tc.isDelete {
					_, err = rootProxy.Logical().Delete("secret/metadata/kv-key-conflict-tests/" + tc.targetSecretName)
				} else {
					err = rootProxy.UpsertKV("secret/kv-key-conflict-tests/"+tc.targetSecretName, tc.data)
				}
				if err != nil {
					responseErr, ok := err.(*api.ResponseError)
					if !ok {
						t.Fatalf("got an error back that was not a response error but a %T", err)
					}
					actualStatusCode = responseErr.StatusCode
					actualErrors = responseErr.Errors
				}
				if tc.expectedStatusCode != actualStatusCode {
					t.Errorf("expected status code %d, got status code %d", tc.expectedStatusCode, actualStatusCode)
				}
				if diff := cmp.Diff(actualErrors, tc.expectedErrors); diff != "" {
					t.Fatalf("actual errors differ from expected: %s", diff)
				}
			})
		}
	})

}
func writeKV(client *api.Client, path string, data map[string]string) error {
	request := client.NewRequest("POST", path)
	body := map[string]map[string]string{
		"data": data,
	}
	serializedBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to serialize body: %w", err)
	}
	request.BodyBytes = serializedBody
	if _, err := client.RawRequest(request); err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return nil
}
