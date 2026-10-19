package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

// testDispatcherTokens supplies changeable credentials without a filesystem watcher.
type testDispatcherTokens struct {
	control     string
	observation string
}

// controlToken provides the current simulated control credential.
func (tokens *testDispatcherTokens) controlToken() []byte { return []byte(tokens.control) }

// observationToken provides the current simulated dashboard credential.
func (tokens *testDispatcherTokens) observationToken() []byte { return []byte(tokens.observation) }

func TestObservationCredentialCannotControlDispatcher(t *testing.T) {
	for _, tc := range []struct {
		name        string
		credential  string
		observation bool
		collide     bool
		wantCode    int
	}{
		{name: "observer reads state", credential: "reader", observation: true, wantCode: http.StatusOK},
		{name: "observer cannot apply", credential: "reader", wantCode: http.StatusUnauthorized},
		{name: "control credential reaches request validation", credential: "writer", wantCode: http.StatusBadRequest},
		{name: "control token cannot observe", credential: "writer", observation: true, wantCode: http.StatusUnauthorized},
		{name: "rotation collision cannot apply", credential: "reader", collide: true, wantCode: http.StatusUnauthorized},
		{name: "rotation collision retains observation", credential: "reader", observation: true, collide: true, wantCode: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := &testDispatcherTokens{control: "writer", observation: "reader"}
			tokens := dispatcherAPITokens{control: values.controlToken, observation: values.observationToken}
			control, err := dispatcher.NewControlPlane(dispatcher.NewSnapshotManager(""), dispatcher.NewMemoryOverrideStore(), dispatcher.ControlOptions{AllowedChannelID: "channel"})
			if err != nil {
				t.Fatal(err)
			}
			var handler http.Handler = dispatcher.NewControlServer(control, tokens.controlToken)
			method, path := http.MethodPost, "/control/v1/plans/id/apply"
			if tc.observation {
				handler = dispatcher.NewObservationServer(dispatcher.NewSnapshotManager(""), nil, tokens.observation)
				method, path = http.MethodGet, "/observe/v1/state"
			}
			if tc.collide {
				values.control = "reader"
			}
			request := httptest.NewRequest(method, path, nil)
			request.Header.Set("Authorization", "Bearer "+tc.credential)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.wantCode {
				t.Errorf("expected HTTP %d, got %d: %s", tc.wantCode, response.Code, response.Body.String())
			}
		})
	}
}
