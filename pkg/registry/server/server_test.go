package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/prow/pkg/metrics"
)

var configresolverMetrics = metrics.NewMetrics("unittest")

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
