package load

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ghodss/yaml"

	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
)

func TestRegistry(t *testing.T) {
	defaultStr := "test parameter default"
	var (
		expectedReferences = registry.ReferenceByName{
			"ipi-deprovision-deprovision": {
				As:       "ipi-deprovision-deprovision",
				From:     "installer",
				Commands: "openshift-cluster destroy\n",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
				},
			},
			"ipi-deprovision-must-gather": {
				As:       "ipi-deprovision-must-gather",
				From:     "installer",
				Commands: "gather\n",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
				},
			},
			"ipi-install-install": {
				As:       "ipi-install-install",
				From:     "installer",
				Commands: "openshift-cluster install\n",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
				},
				Environment: []api.StepParameter{
					{Name: "TEST_PARAMETER", Default: &defaultStr},
				},
				Observers: []string{"resourcewatcher"},
			},
			"ipi-install-rbac": {
				As:       "ipi-install-rbac",
				From:     "installer",
				Commands: "setup-rbac\n",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
				},
			},
		}

		deprovisionRef       = `ipi-deprovision-deprovision`
		deprovisionGatherRef = `ipi-deprovision-must-gather`
		installRef           = `ipi-install-install`
		installRBACRef       = `ipi-install-rbac`
		installChain         = `ipi-install`

		chainDefault   = "test parameter set by chain"
		defaultEmpty   = ""
		expectedChains = registry.ChainByName{
			"ipi-install": api.RegistryChain{
				As: "ipi-install",
				Steps: []api.TestStep{
					{
						Reference: &installRBACRef,
					}, {
						Reference: &installRef,
					},
				},
			},
			"ipi-install-empty-parameter": {
				As:          "ipi-install-empty-parameter",
				Steps:       []api.TestStep{{Chain: &installChain}},
				Environment: []api.StepParameter{{Name: "TEST_PARAMETER", Default: &defaultEmpty}},
			},
			"ipi-install-with-parameter": api.RegistryChain{
				As:    "ipi-install-with-parameter",
				Steps: []api.TestStep{{Chain: &installChain}},
				Environment: []api.StepParameter{{
					Name:    "TEST_PARAMETER",
					Default: &chainDefault,
				}},
			},
			"ipi-deprovision": api.RegistryChain{
				As: "ipi-deprovision",
				Steps: []api.TestStep{
					{
						Reference: &deprovisionGatherRef,
					}, {
						Reference: &deprovisionRef,
					},
				},
			},
		}

		deprovisionChain = `ipi-deprovision`

		expectedWorkflows = registry.WorkflowByName{
			"ipi": {
				Pre: []api.TestStep{{
					Chain: &installChain,
				}},
				Post: []api.TestStep{{
					Chain: &deprovisionChain,
				}},
				Observers: &api.Observers{Disable: []string{"resourcewatcher"}},
			},
			"ipi-changed": {
				Pre: []api.TestStep{{
					Chain: &installChain,
				}},
				Post: []api.TestStep{{
					Chain: &deprovisionChain,
				}},
				Observers: &api.Observers{Disable: []string{"resourcewatcher"}},
			},
		}

		expectedObservers = registry.ObserverByName{
			"resourcewatcher": {
				Name:      "resourcewatcher",
				FromImage: &api.ImageStreamTagReference{Namespace: "ocp", Name: "resourcewatcher", Tag: "latest"},
				Commands:  "#!/bin/bash\n\nsleep 300",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
				},
			},
		}

		testCases = []struct {
			name          string
			registryDir   string
			flags         RegistryFlag
			references    registry.ReferenceByName
			chains        registry.ChainByName
			workflows     registry.WorkflowByName
			observers     registry.ObserverByName
			expectedError bool
		}{{
			name:          "Read registry",
			registryDir:   "../../test/multistage-registry/registry",
			references:    expectedReferences,
			chains:        expectedChains,
			workflows:     expectedWorkflows,
			observers:     expectedObservers,
			expectedError: false,
		}, {
			name:        "Read configmap style registry",
			registryDir: "../../test/multistage-registry/configmap",
			flags:       RegistryFlat,
			references: registry.ReferenceByName{
				"ipi-install-install": {
					As:       "ipi-install-install",
					From:     "installer",
					Commands: "openshift-cluster install\n",
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
					},
					Environment: []api.StepParameter{
						{Name: "TEST_PARAMETER", Default: &defaultStr},
					},
				},
			},
			chains:        registry.ChainByName{},
			workflows:     registry.WorkflowByName{},
			observers:     registry.ObserverByName{},
			expectedError: false,
		}, {
			name:          "Read registry with ref where name and filename don't match",
			registryDir:   "../../test/multistage-registry/invalid-filename",
			references:    nil,
			chains:        nil,
			workflows:     nil,
			expectedError: true,
		}, {
			name:          "Read registry where ref has an extra, invalid field",
			registryDir:   "../../test/multistage-registry/invalid-field",
			references:    nil,
			chains:        nil,
			workflows:     nil,
			expectedError: true,
		}, {
			name:          "Read registry where ref has command containing trap without grace period specified",
			registryDir:   "../../test/multistage-registry/trap-without-grace-period",
			references:    nil,
			chains:        nil,
			workflows:     nil,
			expectedError: true,
		}, {
			name:          "Read registry where ref has best effort defined without timeout",
			registryDir:   "../../test/multistage-registry/best-effort-without-timeout",
			references:    nil,
			chains:        nil,
			workflows:     nil,
			expectedError: true,
		}}
	)

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			references, chains, workflows, _, _, observers, err := Registry(testCase.registryDir, testCase.flags)
			if err == nil && testCase.expectedError == true {
				t.Error("got no error when error was expected")
			}
			if err != nil && testCase.expectedError == false {
				t.Errorf("got error when error wasn't expected: %v", err)
			}
			if !reflect.DeepEqual(references, testCase.references) {
				t.Errorf("output references different from expected: %s", diff.ObjectReflectDiff(references, testCase.references))
			}
			if !reflect.DeepEqual(chains, testCase.chains) {
				t.Errorf("output chains different from expected: %s", diff.ObjectReflectDiff(chains, testCase.chains))
			}
			if !reflect.DeepEqual(workflows, testCase.workflows) {
				t.Errorf("output workflows different from expected: %s", diff.ObjectReflectDiff(workflows, testCase.workflows))
			}
			if !reflect.DeepEqual(observers, testCase.observers) {
				t.Errorf("output observers different from expected: %s", diff.ObjectReflectDiff(observers, testCase.observers))
			}
		})
	}
	// set up a temporary directory registry with a broken component
	temp, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("failed to create temp step registry: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(temp); err != nil {
			t.Fatalf("failed to remove temp step registry: %v", err)
		}
	}()

	// create directory with slightly incorrect path based on ref name
	path := filepath.Join(temp, "ipi/deprovision/gather")
	err = os.MkdirAll(path, 0755)
	if err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	fileData, err := yaml.Marshal(expectedChains[deprovisionGatherRef])
	if err != nil {
		t.Fatalf("failed to marshal %s into a yaml []byte: %v", deprovisionGatherRef, err)
	}

	if err := ioutil.WriteFile(filepath.Join(path, deprovisionGatherRef), fileData, 0664); err != nil {
		t.Fatalf("failed to populate temp reference file: %v", err)
	}
	_, _, _, _, _, _, err = Registry(temp, RegistryFlag(0))
	if err == nil {
		t.Error("got no error when expecting error on incorrect reference name")
	}
}
