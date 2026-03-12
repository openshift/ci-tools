package steps

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
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
				leaseClient := lease.NewFakeClient("owner", "", 1, nil, nil, nil)
				return LeaseProxyStep(nil, "x.y.w.z", &http.ServeMux{}, &leaseClient)
			},
		},
		{
			name: "http mux is missing",
			newLeaseProxyStepFunc: func() api.Step {
				return LeaseProxyStep(nil, "x.y.w.z", nil, nil)
			},
			wantErr: errors.New("lease proxy server requires an HTTP server mux"),
		},
		{
			name: "http address is empty",
			newLeaseProxyStepFunc: func() api.Step {
				leaseClient := lease.NewFakeClient("owner", "", 1, nil, nil, nil)
				return LeaseProxyStep(nil, "", &http.ServeMux{}, &leaseClient)
			},
			wantErr: errors.New("lease proxy server requires an HTTP server address"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotErr := tc.newLeaseProxyStepFunc().Validate()

			cmpError(t, tc.wantErr, gotErr)
		})
	}
}

func cmpError(t *testing.T, want, got error) {
	if got != nil && want == nil {
		t.Errorf("want err nil but got: %v", got)
	}
	if got == nil && want != nil {
		t.Errorf("want err %v but nil", want)
	}
	if got != nil && want != nil {
		if diff := cmp.Diff(want.Error(), got.Error()); diff != "" {
			t.Errorf("unexpected error: %s", diff)
		}
	}
}
