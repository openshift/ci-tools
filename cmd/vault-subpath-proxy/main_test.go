package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/vaultclient"
)

const (
	vaultTestingToken = "jpuxZFWWFW7vM882GGX2aWOE"
)

func TestProxy(tt *testing.T) {
	tt.Parallel()
	logrus.SetLevel(logrus.TraceLevel)

	ctx, cancel := context.WithCancel(context.Background())
	t := testhelper.NewT(ctx, tt)
	vaultAddr := testhelper.Vault(ctx, t)

	vaultClient, err := api.NewClient(&api.Config{Address: "http://" + vaultAddr})
	if err != nil {
		t.Fatalf("failed to construct vault client: %v", err)
	}
	vaultClient.SetToken(vaultTestingToken)

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
	proxyServerPort := testhelper.GetFreePort(t)
	proxyServer, err := createProxyServer("http://"+vaultAddr, "127.0.0.1:"+proxyServerPort, "secret")
	if err != nil {
		t.Fatalf("failed to create proxy server: %v", err)
	}

	go func() {
		if err := proxyServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			cancel()
			t.Errorf("proxy server failed to listen: %v", err)
		}
	}()
	t.Cleanup(func() {
		if err := proxyServer.Close(); err != nil {
			t.Errorf("failed to close proxy: %v", err)
		}
	})
	testhelper.WaitForHTTP200("http://127.0.0.1:"+proxyServerPort+"/v1/sys/health", "vault-subpath-proxy", t)

	rootDirect, err := vaultclient.New("http://"+vaultAddr, vaultTestingToken)
	if err != nil {
		t.Fatalf("failed to construct rootDirect client: %v", err)
	}
	rootProxy, err := vaultclient.New("http://127.0.0.1:"+proxyServerPort, vaultTestingToken)
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
	}{
		{
			name:               "Slash in key name is refused",
			data:               map[string]string{"invalid/key": "data"},
			expectedStatusCode: 400,
			expectedErrors:     []string{"key invalid/key is invalid: [a lowercase RFC 1123 label must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character (e.g. 'my-name',  or '123-abc', regex used for validation is '[a-z0-9]([-a-z0-9]*[a-z0-9])?')]"},
		},
		{
			name: "All letters key is allowed",
			data: map[string]string{"key": "data"},
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
	}
	for i, tc := range kvKeyValidationTestCases {
		t.Run(tc.name, func(t *testing.T) {

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
		})
	}

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
