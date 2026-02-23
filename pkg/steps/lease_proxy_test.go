package steps

import (
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
