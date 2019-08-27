package registry

import (
	"reflect"
	"testing"

	api "github.com/openshift/ci-tools/pkg/api"
	types "github.com/openshift/ci-tools/pkg/steps/types"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestResolve(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		config      api.MultiStageTestConfiguration
		expectedRes types.TestFlow
		expectErr   bool
	}{{
		// This is a full config that should not change (other than struct) when passed to the Resolver
		name: "Full AWS IPI",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Pre: []api.TestStep{{
				Name:     "ipi-install",
				Image:    "installer",
				Commands: "openshift-cluster install",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Test: []api.TestStep{{
				Name:     "e2e",
				Image:    "my-image",
				Commands: "make custom-e2e",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Post: []api.TestStep{{
				Name:     "ipi-teardown",
				Image:    "installer",
				Commands: "openshift-cluster destroy",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
		},
		expectedRes: types.TestFlow{
			ClusterProfile: api.ClusterProfileAWS,
			Pre: []types.TestStep{{
				Name:     "ipi-install",
				Image:    "installer",
				Commands: "openshift-cluster install",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Test: []types.TestStep{{
				Name:     "e2e",
				Image:    "my-image",
				Commands: "make custom-e2e",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Post: []types.TestStep{{
				Name:     "ipi-teardown",
				Image:    "installer",
				Commands: "openshift-cluster destroy",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
		},
		expectErr: false,
	}, {}} {
		t.Run(testCase.name, func(t *testing.T) {
			ret, err := NewResolver().Resolve(testCase.config)
			if !testCase.expectErr && err != nil {
				t.Errorf("%s: expected no error but got: %s", testCase.name, err)
			}
			if testCase.expectErr && err == nil {
				t.Errorf("%s: expected error but got none", testCase.name)
			}
			if !reflect.DeepEqual(ret, testCase.expectedRes) {
				t.Errorf("%s: fo incorrect output: %s", testCase.name, diff.ObjectReflectDiff(ret, testCase.expectedRes))
			}
		})
	}
}
