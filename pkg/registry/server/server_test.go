package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/prow/pkg/metrics"

	"github.com/openshift/ci-tools/pkg/api"
)

var configresolverMetrics = metrics.NewMetrics("unittest")

type mergeFailureGetter struct{ err error }

func (getter mergeFailureGetter) GetMatchingConfig(metadata api.Metadata) (api.ReleaseBuildConfiguration, error) {
	return api.ReleaseBuildConfiguration{}, getter.err
}

func TestMergeInjectedTestFailures(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		query  string
		getter Getter
		status int
	}{
		{name: "invalid injection", status: http.StatusBadRequest},
		{name: "missing source config", query: "&injectTest=test&injectTestFromOrg=org&injectTestFromRepo=source&injectTestFromBranch=main", getter: mergeFailureGetter{err: errors.New("missing")}, status: http.StatusNotFound},
		{name: "missing selected test", query: "&injectTest=test&injectTestFromOrg=org&injectTestFromRepo=source&injectTestFromBranch=main", getter: mergeFailureGetter{}, status: http.StatusBadRequest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/?org=org&repo=component&branch=main"+testCase.query, nil)
			response := httptest.NewRecorder()
			ResolveAndMergeConfigsAndInjectTest(testCase.getter, nil, configresolverMetrics).ServeHTTP(response, request)
			if response.Code != testCase.status {
				t.Errorf("status=%d, want %d: %s", response.Code, testCase.status, response.Body.String())
			}
		})
	}
}

func TestResolveAndMergeConfigsAndInjectTest(t *testing.T) {

	var testCases = []struct {
		name         string
		url          string
		configs      Getter
		resolver     Resolver
		expectedCode int
		expectedBody string
	}{
		{
			name:         "invalid variants",
			url:          "https://config.ci.openshift.org/mergeConfigsWithInjectedTest?branch=master%2Cmain&injectTest=e2e-aws-ovn-upgrade-ipsec&injectTestFromBranch=master&injectTestFromOrg=openshift&injectTestFromRepo=ovn-kubernetes&injectTestFromVariant=4.21-upgrade-from-stable-4.20&org=openshift%2Copenshift&repo=ovn-kubernetes%2Corigin&variant=4.21-upgrade-from-stable-4.20",
			expectedCode: http.StatusBadRequest,
			expectedBody: "If any variants are passed, there must be one for each ref. Blank variants are allowed.",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", testCase.url, nil)
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()
			handler := ResolveAndMergeConfigsAndInjectTest(testCase.configs, testCase.resolver, configresolverMetrics)

			handler.ServeHTTP(rr, req)

			if diff := cmp.Diff(testCase.expectedCode, rr.Code); diff != "" {
				t.Errorf("error differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(testCase.expectedBody, rr.Body.String()); diff != "" {
				t.Errorf("error differs from expected:\n%s", diff)
			}
		})
	}

}
