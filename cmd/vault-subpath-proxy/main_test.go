package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

const (
	vaultTestingToken = "jpuxZFWWFW7vM882GGX2aWOE"
	vaultTestingPort  = 8300
)

func TestProxy(t *testing.T) {
	if _, err := exec.LookPath("vault"); err != nil {
		if _, runningInCi := os.LookupEnv("CI"); runningInCi {
			t.Fatalf("could not find vault in path: %v", err)
		}
		t.Skip("could not find vault in path")
	}
	if os.Getenv("CI") != "" {
		// We need a writeable home
		os.Setenv("HOME", "/tmp")
	}
	logrus.SetLevel(logrus.TraceLevel)

	vaultCancel, vaultDone, err := startVault(t)
	if err != nil {
		t.Fatalf("failed to start vault: %v", err)
	}
	t.Cleanup(func() {
		vaultCancel()
		<-vaultDone
	})

	vaultClient, err := api.NewClient(&api.Config{Address: "http://127.0.0.1:8300"})
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
	proxyServer, err := createProxyServer("http://127.0.0.1:8300", "127.0.0.1:8400", "secret")
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
	if err := waitForVaultReady(context.Background(), t, 8400); err != nil {
		t.Fatalf("failed waiting for vault proxy to be ready: %v", err)
	}

	rootDirect, err := vaultClientFor("http://127.0.0.1:8300", vaultTestingToken, "root")
	if err != nil {
		t.Fatalf("failed to construct rootDirect client: %v", err)
	}
	rootProxy, err := vaultClientFor("http://127.0.0.1:8400", vaultTestingToken, "root")
	if err != nil {
		t.Fatalf("failed to construct rootProxy client: %v", err)
	}
	team1Direct, err := vaultClientFor("http://127.0.0.1:8300", team1TokenResponse.Auth.ClientToken, "team-1")
	if err != nil {
		t.Fatalf("failed to construct team1Direct client: %v", err)
	}
	team1Proxy, err := vaultClientFor("http://127.0.0.1:8400", team1TokenResponse.Auth.ClientToken, "team-1")
	if err != nil {
		t.Fatalf("failed to construct team1Proxy client: %v", err)
	}

	type clientTestCase struct {
		clientName         string
		client             *api.Client
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
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
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

}

func vaultClientFor(url, token, userAgent string) (*api.Client, error) {
	client, err := api.NewClient(&api.Config{Address: url})
	if err != nil {
		return nil, fmt.Errorf("failed to construct client: %w", err)
	}
	client.SetToken(token)
	client.SetHeaders(http.Header{"user-agent": []string{userAgent}})
	return client, nil
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

func startVault(t *testing.T) (cancel context.CancelFunc, done chan struct{}, err error) {
	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, "vault",
		"server",
		"-dev",
		fmt.Sprintf("--dev-listen-address=127.0.0.1:%d", vaultTestingPort),
		fmt.Sprintf("-dev-root-token-id=%s", vaultTestingToken),
	)

	done = make(chan struct{})
	gotReady := make(chan struct{})
	go func() {
		defer close(done)
		out, err := cmd.CombinedOutput()
		if err != nil && !isChannelClosed(gotReady) {
			t.Errorf("vault command failed: err: %v, out:\n%s\n", err, string(out))
		}
	}()

	if err := waitForVaultReady(ctx, t, vaultTestingPort); err != nil {
		cancel()
		// Let the other goroutine print the log
		<-done
		return nil, nil, errors.New("timed out waiting for vault to get ready")
	}

	close(gotReady)
	return cancel, done, nil
}

func isChannelClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func waitForVaultReady(ctx context.Context, t *testing.T, targetPort int) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var errs []error
	for ctx.Err() == nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/sys/health", targetPort), nil)
		if err != nil {
			t.Fatalf("failed to construct http request for vaulth healthcheck: %v", err)
		}
		response, err := http.DefaultClient.Do(request)
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		if err == nil && response.StatusCode == http.StatusOK {
			return nil
		}
		errs = append(errs, err)
	}

	return fmt.Errorf("reached thirty second time out, errors when healthchecking: %w", utilerrors.NewAggregate(errs))
}
