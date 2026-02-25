package steps

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestLeaseProxyProvides(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		httpSrvAddr    string
		expectedParams map[string]string
	}{
		{
			name:           "Empty HTTP server addr",
			expectedParams: map[string]string{api.LeaseProxyServerURLEnvVarName: ""},
		},
		{
			name:           "Non empty HTTP server addr",
			httpSrvAddr:    "http://10.0.0.1:8080",
			expectedParams: map[string]string{api.LeaseProxyServerURLEnvVarName: "http://10.0.0.1:8080"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			step := LeaseProxyStep(logrus.NewEntry(&logrus.Logger{}), tc.httpSrvAddr, &http.ServeMux{}, nil)

			gotParams := make(map[string]string)
			for k, f := range step.Provides() {
				v, err := f()
				if err != nil {
					t.Fatalf("get param %s: %s", k, err)
				}
				gotParams[k] = v
			}

			if diff := cmp.Diff(tc.expectedParams, gotParams); diff != "" {
				t.Errorf("unexpected params: %s", diff)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                  string
		newLeaseProxyStepFunc func() api.Step
		wantErr               error
	}{
		{
			name: "Validation passes",
			newLeaseProxyStepFunc: func() api.Step {
				return &stepLeaseProxyServer{logger: nil, srvMux: &http.ServeMux{}, srvAddr: "x.y.w.z"}
			},
		},
		{
			name: "http mux is missing",
			newLeaseProxyStepFunc: func() api.Step {
				return &stepLeaseProxyServer{logger: nil, srvMux: nil, srvAddr: "x.y.w.z"}
			},
			wantErr: errors.New("lease proxy server requires an HTTP server mux"),
		},
		{
			name: "http address is empty",
			newLeaseProxyStepFunc: func() api.Step {
				return &stepLeaseProxyServer{logger: nil, srvMux: &http.ServeMux{}, srvAddr: ""}
			},
			wantErr: errors.New("lease proxy server requires an HTTP server address"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotErr := tc.newLeaseProxyStepFunc().Validate()

			switch {
			case gotErr == nil && tc.wantErr == nil:
				break

			case gotErr == nil && tc.wantErr != nil:
				t.Errorf("expected %q but got nil", tc.wantErr.Error())

			case gotErr != nil && tc.wantErr == nil:
				t.Errorf("expected nil error but got %q", gotErr.Error())

			case gotErr.Error() != tc.wantErr.Error():
				t.Errorf("want %q but got %q", tc.wantErr.Error(), gotErr.Error())
			}
		})
	}
}
