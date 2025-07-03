package configresolver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

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
		expected    *IntegratedStream
		expectedErr error
	}{
		{
			name:     "basic case",
			isNS:     "ocp",
			isName:   "4.15",
			client:   fakeclient.NewClientBuilder().WithRuntimeObjects(ocp415Stream.DeepCopy()).Build(),
			expected: &IntegratedStream{Tags: []string{"bar", "foo"}, ReleaseControllerConfigName: "4.15.0-0.ci"},
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
			expected: &IntegratedStream{Tags: []string{"bar", "foo"}, ReleaseControllerConfigName: ""},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, _, actualErr := LocalIntegratedStream(context.Background(), tc.client, tc.isNS, tc.isName)
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
