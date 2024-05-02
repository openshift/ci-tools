package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api/configresolver"
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
			name:   "ocp/4.9 is valid",
			isNS:   "ocp",
			isName: "4.9",
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
			name:     "origin/bar-4.15 is invalid",
			isNS:     "origin",
			isName:   "bar-4.15",
			expected: errors.New("not a valid integrated stream: origin/bar-4.15"),
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

var ocp415Stream = &imagev1.ImageStream{
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
}

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestIntegratedStream(t *testing.T) {
	ocp51Stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "5.1",
			Namespace: "ocp",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "bar"},
				{Tag: "foo"},
			},
		},
	}

	testCases := []struct {
		name        string
		client      ctrlruntimeclient.Client
		isNS        string
		isName      string
		expected    *configresolver.IntegratedStream
		expectedErr error
	}{
		{
			name:     "basic case",
			isNS:     "ocp",
			isName:   "4.15",
			client:   fakeclient.NewClientBuilder().WithRuntimeObjects(ocp415Stream.DeepCopy()).Build(),
			expected: &configresolver.IntegratedStream{Tags: []string{"bar", "foo"}, ReleaseControllerConfigName: "4.15.0-0.ci"},
		},
		{
			name:        "not found",
			isNS:        "ocp",
			isName:      "3.15",
			client:      fakeclient.NewClientBuilder().WithRuntimeObjects().Build(),
			expectedErr: errors.New("failed to get image stream ocp/3.15: imagestreams.image.openshift.io \"3.15\" not found"),
		},
		{
			name:     "no annotation",
			isNS:     "ocp",
			isName:   "5.1",
			client:   fakeclient.NewClientBuilder().WithRuntimeObjects(ocp51Stream.DeepCopy()).Build(),
			expected: &configresolver.IntegratedStream{Tags: []string{"bar", "foo"}, ReleaseControllerConfigName: ""},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := integratedStream(context.Background(), tc.client, tc.isNS, tc.isName)
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if actualErr == nil {
				if diff := cmp.Diff(tc.expected, actual); diff != "" {
					t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
				}
			}
		})
	}
}

func TestGetIntegratedStream(t *testing.T) {
	testCases := []struct {
		name         string
		client       ctrlruntimeclient.Client
		url          string
		expectedCode int
		expectedBody string
	}{
		{
			name:         "basic case",
			client:       fakeclient.NewClientBuilder().WithRuntimeObjects(ocp415Stream.DeepCopy()).Build(),
			url:          "/url?namespace=ocp&name=4.15",
			expectedCode: 200,
			expectedBody: "{\"tags\":[\"bar\",\"foo\"],\"releaseControllerConfigName\":\"4.15.0-0.ci\"}\n",
		},
		{
			name:         "aaa",
			client:       fakeclient.NewClientBuilder().WithRuntimeObjects(ocp415Stream.DeepCopy()).Build(),
			url:          "/url?namespace=ocp&name=5.1",
			expectedCode: 400,
			expectedBody: "not a valid integrated stream: ocp/5.1\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			handlerFunc := getIntegratedStream(context.Background(), tc.client)
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
