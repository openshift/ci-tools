package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/prow/pkg/metrics"

	"github.com/openshift/ci-tools/pkg/api"
)

var configresolverMetrics = metrics.NewMetrics("unittest")

// fakeConfigGetter maps repository metadata to test configurations.
type fakeConfigGetter map[api.Metadata]api.ReleaseBuildConfiguration

// GetMatchingConfig returns the configuration for metadata or an error if it is missing.
func (g fakeConfigGetter) GetMatchingConfig(metadata api.Metadata) (api.ReleaseBuildConfiguration, error) {
	config, ok := g[metadata]
	if !ok {
		return api.ReleaseBuildConfiguration{}, fmt.Errorf("config not found for %s", metadata.AsString())
	}
	return config, nil
}

// passthroughResolver returns merged test configurations without registry expansion.
type passthroughResolver struct{}

// ResolveConfig returns config unchanged.
func (passthroughResolver) ResolveConfig(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error) {
	return config, nil
}

func TestResolveAndMergeConfigsAndInjectTest(t *testing.T) {
	targetMetadata := api.Metadata{Org: "openshift", Repo: "cluster-capi-operator", Branch: "main"}
	originMetadata := api.Metadata{Org: "openshift", Repo: "api", Branch: "main"}
	targetConfig := api.ReleaseBuildConfiguration{
		InputConfiguration: api.InputConfiguration{
			BuildRootImage: &api.BuildRootImageConfiguration{ImageStreamTagReference: &api.ImageStreamTagReference{
				Namespace: "ci", Name: "target-root", Tag: "latest",
			}},
		},
		Tests: []api.TestStepConfiguration{{As: "e2e"}},
	}
	originImages := map[string]api.ImageStreamTagReference{
		"tools": {Namespace: "ci", Name: "tools", Tag: "latest"},
	}
	mergedImages := map[string]api.ImageStreamTagReference{
		"tools-openshift.api": {Namespace: "ci", Name: "tools", Tag: "latest"},
	}
	mergeURL := "https://config.ci.openshift.org/mergeConfigsWithInjectedTest?org=openshift,openshift&repo=cluster-capi-operator,api&branch=main,main&injectTestFromOrg=openshift&injectTestFromRepo=cluster-capi-operator&injectTestFromBranch=main&injectTest=e2e"

	var testCases = []struct {
		name           string
		url            string
		configs        Getter
		resolver       Resolver
		expectedCode   int
		expectedBody   string
		expectedConfig *api.ReleaseBuildConfiguration
	}{
		{
			name:         "invalid variants",
			url:          "https://config.ci.openshift.org/mergeConfigsWithInjectedTest?branch=master%2Cmain&injectTest=e2e-aws-ovn-upgrade-ipsec&injectTestFromBranch=master&injectTestFromOrg=openshift&injectTestFromRepo=ovn-kubernetes&injectTestFromVariant=4.21-upgrade-from-stable-4.20&org=openshift%2Copenshift&repo=ovn-kubernetes%2Corigin&variant=4.21-upgrade-from-stable-4.20",
			expectedCode: http.StatusBadRequest,
			expectedBody: "If any variants are passed, there must be one for each ref. Blank variants are allowed.",
		},
		{
			name: "origin without build root still contributes other inputs",
			url:  mergeURL,
			configs: fakeConfigGetter{
				targetMetadata: targetConfig,
				originMetadata: {InputConfiguration: api.InputConfiguration{BaseImages: originImages}},
			},
			resolver:     passthroughResolver{},
			expectedCode: http.StatusOK,
			expectedConfig: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: targetConfig.BuildRootImage,
					BaseImages:     mergedImages,
				},
				Tests: targetConfig.Tests,
			},
		},
		{
			name: "origin build root is merged without replacing target build root",
			url:  mergeURL,
			configs: fakeConfigGetter{
				targetMetadata: targetConfig,
				originMetadata: {InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{FromRepository: true},
					BaseImages:     originImages,
				}},
			},
			resolver:     passthroughResolver{},
			expectedCode: http.StatusOK,
			expectedConfig: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: targetConfig.BuildRootImage,
					BuildRootImages: map[string]api.BuildRootImageConfiguration{
						"openshift.api": {FromRepository: true},
					},
					BaseImages: mergedImages,
				},
				Tests: targetConfig.Tests,
			},
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
			if testCase.expectedConfig != nil {
				var config api.ReleaseBuildConfiguration
				if err := json.Unmarshal(rr.Body.Bytes(), &config); err != nil {
					t.Fatalf("decoding merged config: %v", err)
				}
				if diff := cmp.Diff(testCase.expectedConfig, &config); diff != "" {
					t.Errorf("unexpected merged config (-want +got):\n%s", diff)
				}
				return
			}
			if diff := cmp.Diff(testCase.expectedBody, rr.Body.String()); diff != "" {
				t.Errorf("error differs from expected:\n%s", diff)
			}
		})
	}

}
