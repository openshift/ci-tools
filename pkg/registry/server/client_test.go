package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestConfig(t *testing.T) {
	var config api.ReleaseBuildConfiguration
	rawConfig := testhelper.ReadFromFixture(t, "served config")
	if err := yaml.UnmarshalStrict(rawConfig, &config); err != nil {
		t.Fatal("failed to unmarshal fixture config: %w", err)
	}

	correctHandler := func(t *testing.T, jsonConfig []byte) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("org") != "openshift" {
				t.Errorf("%s: Org should equal openshift, but was %s", t.Name(), r.URL.Query().Get("org"))
			}
			if r.URL.Query().Get("repo") != "hyperkube" {
				t.Errorf("%s: Repo should equal hyperkube, but was %s", t.Name(), r.URL.Query().Get("repo"))
			}
			if r.URL.Query().Get("branch") != "master" {
				t.Errorf("%s: Branch should equal master, but was %s", t.Name(), r.URL.Query().Get("branch"))
			}
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write(jsonConfig); err != nil {
				t.Errorf("failed to write data: %v", err)
			}
		})
	}
	failingHandler := func(t *testing.T, jsonConfig []byte) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			if _, err := w.Write([]byte("FFFFFFUUUUUUU")); err != nil {
				t.Errorf("failed to write: %v", err)
			}
		})
	}
	var testCases = []struct {
		name           string
		handlerWrapper func(t *testing.T, jsonConfig []byte) http.Handler
		expected       *api.ReleaseBuildConfiguration
		expectedError  error
	}{
		{
			name:           "getting config works",
			handlerWrapper: correctHandler,
		},
		{
			name:           "function errors on non OK status",
			handlerWrapper: failingHandler,
			expectedError:  errors.New("got unexpected http 400 status code from configresolver: FFFFFFUUUUUUU"),
		},
	}

	jsonConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("%s: Failed to marshal parsedConfig to JSON: %v", t.Name(), err)
	}
	metadata := api.Metadata{Org: "openshift", Repo: "hyperkube", Branch: "master"}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(testCase.handlerWrapper(t, jsonConfig))
			client := resolverClient{Address: server.URL}
			config, err := client.Config(&metadata)
			if diff := cmp.Diff(testCase.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%s", diff)
			}
			if testCase.expectedError == nil {
				testhelper.CompareWithFixture(t, config)
			}
			server.Close()
		})
	}
}
