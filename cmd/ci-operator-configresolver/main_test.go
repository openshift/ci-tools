package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api/configresolver"
	registryserver "github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestValidateStream(t *testing.T) {
	testCases := []struct {
		name     string
		isNS     string
		isName   string
		expected error
	}{
		{
			name:     "namespace cannot be empty",
			expected: errors.New("namespace cannot be empty"),
		},
		{
			name:     "namespace cannot be empty",
			isNS:     "ns",
			expected: errors.New("name cannot be empty"),
		},
		{
			name:     "namespace cannot be empty",
			isNS:     "ns",
			isName:   "is",
			expected: errors.New("not a valid integrated stream: ns/is"),
		},
		{
			name:     "origin/4.3 is invalid",
			isNS:     "origin",
			isName:   "4.3",
			expected: errors.New("not a valid integrated stream: origin/4.3"),
		},
		{
			name:   "ocp/4.15 is valid",
			isNS:   "ocp",
			isName: "4.15",
		},
		{
			name:   "origin/4.15 is valid",
			isNS:   "origin",
			isName: "4.15",
		},
		{
			name:   "origin/scos-4.15 is valid",
			isNS:   "origin",
			isName: "scos-4.15",
		},
		{
			name:   "ocp-priv/4.16 is valid",
			isNS:   "ocp-priv",
			isName: "4.16",
		},
		{
			name:     "origin/bar-4.15 is invalid",
			isNS:     "origin",
			isName:   "bar-4.15",
			expected: errors.New("not a valid integrated stream: origin/bar-4.15"),
		},
		{
			name:   "origin/sriov-4.15 is valid",
			isNS:   "origin",
			isName: "sriov-4.15",
		},
		{
			name:   "origin/metallb-4.15 is valid",
			isNS:   "origin",
			isName: "metallb-4.15",
		},
		{
			name:   "origin/ptp-4.15 is valid",
			isNS:   "origin",
			isName: "ptp-4.15",
		},
		{
			name:   "ocp/5.0 is valid",
			isNS:   "ocp",
			isName: "5.0",
		},
		{
			name:   "ocp/5.11 is valid",
			isNS:   "ocp",
			isName: "5.11",
		},
		{
			name:   "ocp/9.99 is valid",
			isNS:   "ocp",
			isName: "5.11",
		},
		{
			name:     "ocp/10.0 is not valid",
			isNS:     "ocp",
			isName:   "10.0",
			expected: errors.New("not a valid integrated stream: ocp/10.0"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := validateStream(tc.isNS, tc.isName)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

var (
	streams = []ctrlruntimeclient.Object{
		&imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "4.15",
				Namespace: "ocp",
				Annotations: map[string]string{
					"release.openshift.io/config": `{"name":"4.15.0-0.ci","to":"release","message":"This release contains CI image builds of all code in release-4.15 (master) branches, and is updated each time someone merges.","mirrorPrefix":"4.15","expires":"72h","maxUnreadyReleases":1,"minCreationIntervalSeconds":21600,"pullSecretName":"source","check":{},"publish":{"tag":{"tagRef":{"name":"4.15-ci"}}},"verify":{"aws-sdn-serial":{"maxRetries":3,"prowJob":{"name":"periodic-ci-openshift-release-master-ci-4.15-e2e-aws-sdn-serial"}},"gcp-sdn":{"optional":true,"prowJob":{"name":"periodic-ci-openshift-release-master-ci-4.15-e2e-gcp-sdn"}},"hypershift-e2e":{"maxRetries":3,"prowJob":{"name":"periodic-ci-openshift-hypershift-release-4.15-periodics-e2e-aws-ovn"},"upgrade":true},"upgrade":{"optional":true,"prowJob":{"name":"periodic-ci-openshift-release-master-ci-4.15-e2e-gcp-sdn-upgrade"},"disabled":true,"upgrade":true},"upgrade-minor-aws-ovn":{"optional":true,"prowJob":{"name":"periodic-ci-openshift-release-master-ci-4.15-upgrade-from-stable-4.14-e2e-aws-ovn-upgrade"},"disabled":true,"upgrade":true,"upgradeFromRelease":{"candidate":{"stream":"ci","version":"4.14"}}},"upgrade-minor-sdn":{"optional":true,"prowJob":{"name":"periodic-ci-openshift-release-master-ci-4.15-upgrade-from-stable-4.14-e2e-aws-sdn-upgrade"},"upgrade":true,"upgradeFromRelease":{"candidate":{"stream":"ci","version":"4.14"}}},"aws-ovn-upgrade-4.15-minor":{"maxRetries":3,"prowJob":{"name":"periodic-ci-openshift-release-master-ci-4.15-upgrade-from-stable-4.14-e2e-aws-ovn-upgrade"},"upgrade":true,"upgradeFromRelease":{"candidate":{"stream":"ci","version":"4.14"}}},"azure-sdn-upgrade-4.15-minor":{"maxRetries":3,"prowJob":{"name":"periodic-ci-openshift-release-master-ci-4.15-upgrade-from-stable-4.14-e2e-azure-sdn-upgrade"},"upgrade":true,"upgradeFromRelease":{"candidate":{"stream":"ci","version":"4.14"}}},"gcp-ovn-upgrade-4.15-micro":{"maxRetries":3,"prowJob":{"name":"periodic-ci-openshift-release-master-ci-4.15-e2e-gcp-ovn-upgrade"},"upgrade":true}}}`,
				},
			},
			Status: imagev1.ImageStreamStatus{
				Tags: []imagev1.NamedTagEventList{
					{Tag: "bar"},
					{Tag: "foo"},
				},
			},
		},
		&imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "5.0",
				Namespace: "ocp",
				Annotations: map[string]string{
					"release.openshift.io/config": `{"name":"5.0.0-0.ci","to":"release-5","message":"This release contains CI image builds of all code in release-5.0 (main) branches, and is updated each time someone merges.","mirrorPrefix":"5.0","expires":"72h","maxUnreadyReleases":1,"minCreationIntervalSeconds":21600,"pullSecretName":"source","alternateImageRepository":"quay.io/openshift-release-dev/dev-release","alternateImageRepositorySecretName":"release-controller-quay-mirror-secret","check":{},"publish":{"tag":{"tagRef":{"name":"5.0-ci"}}},"verify":{}}`,
				},
			},
			Status: imagev1.ImageStreamStatus{
				Tags: []imagev1.NamedTagEventList{
					{Tag: "bar"},
					{Tag: "foo"},
				},
			},
		},
	}
	upstreamStreams = map[string]*configresolver.IntegratedStream{
		"ocp/4.22": {
			Tags:                        []string{"installer"},
			ReleaseControllerConfigName: "4.22.0-0.ci",
		},
	}
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

type fakeResolverClient struct {
	registryserver.ResolverClient
}

func (f *fakeResolverClient) IntegratedStream(namespace, name string) (*configresolver.IntegratedStream, error) {
	nn := namespace + "/" + name
	s, ok := upstreamStreams[nn]
	if !ok {
		return nil, fmt.Errorf("stream %s not found", nn)
	}
	return s, nil
}

func TestGetIntegratedStream(t *testing.T) {
	t.Parallel()
	fakeclient.NewClientBuilder().WithObjects(&imagev1.ImageStream{})

	testCases := []struct {
		name           string
		client         ctrlruntimeclient.Client
		resolverClient *fakeResolverClient
		url            string
		expectedCode   int
		expectedBody   string
	}{
		{
			name:         "4.x stream is valid",
			client:       fakeclient.NewClientBuilder().WithObjects(streams...).Build(),
			url:          "/url?namespace=ocp&name=4.15",
			expectedCode: 200,
			expectedBody: "{\"tags\":[\"bar\",\"foo\"],\"releaseControllerConfigName\":\"4.15.0-0.ci\"}\n",
		},
		{
			name:         "5.x stream is valid",
			client:       fakeclient.NewClientBuilder().WithObjects(streams...).Build(),
			url:          "/url?namespace=ocp&name=5.0",
			expectedCode: 200,
			expectedBody: "{\"tags\":[\"bar\",\"foo\"],\"releaseControllerConfigName\":\"5.0.0-0.ci\"}\n",
		},
		{
			name:         "10.x stream is NOT valid",
			client:       fakeclient.NewClientBuilder().WithObjects(streams...).Build(),
			url:          "/url?namespace=ocp&name=10.0",
			expectedCode: 400,
			expectedBody: "not a valid integrated stream: ocp/10.0\n",
		},
		{
			name:           "4.x stream from upstream resolver",
			resolverClient: &fakeResolverClient{},
			url:            "/url?namespace=ocp&name=4.22",
			expectedCode:   200,
			expectedBody:   "{\"tags\":[\"installer\"],\"releaseControllerConfigName\":\"4.22.0-0.ci\"}\n",
		}, {
			name:           "5.x stream from upstream not found",
			resolverClient: &fakeResolverClient{},
			url:            "/url?namespace=ocp&name=6.22",
			expectedCode:   400,
			expectedBody:   "not a valid integrated stream: ocp/6.22\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequest("GET", tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			var resolver registryserver.ResolverClient
			if tc.resolverClient != nil {
				resolver = tc.resolverClient
			}
			cache := integrationStreamCache(tc.client, resolver, time.Minute)
			handlerFunc := getIntegratedStream(context.Background(), cache)
			handlerFunc.ServeHTTP(rr, req)
			if diff := cmp.Diff(tc.expectedCode, rr.Code); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedBody, rr.Body.String()); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
