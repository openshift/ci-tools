package registry

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/api"
	"k8s.io/apimachinery/pkg/util/diff"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func TestResolve(t *testing.T) {
	reference1 := "generic-unit-test"
	teardownRef := "teardown"
	fipsPreChain := "install-fips"
	nestedChains := "nested-chains"
	chainInstall := "install-chain"
	awsWorkflow := "ipi-aws"
	nonExistentEnv := "NON_EXISTENT"
	stepEnv := "STEP_ENV"
	yes := true
	for _, testCase := range []struct {
		name                  string
		config                api.MultiStageTestConfiguration
		stepMap               ReferenceByName
		chainMap              ChainByName
		workflowMap           WorkflowByName
		expectedRes           api.MultiStageTestConfigurationLiteral
		expectedErr           error
		expectedValidationErr error
	}{{
		// This is a full config that should not change (other than struct) when passed to the Resolver
		name: "Full AWS IPI",
		config: api.MultiStageTestConfiguration{
			ClusterProfile:     api.ClusterProfileAWS,
			AllowSkipOnSuccess: &yes,
			Pre: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:       "ipi-install",
					From:     "installer",
					Commands: "openshift-cluster install",
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "1000m"},
						Limits:   api.ResourceList{"memory": "2Gi"},
					}},
			}},
			Test: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:       "e2e",
					From:     "my-image",
					Commands: "make custom-e2e",
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "1000m"},
						Limits:   api.ResourceList{"memory": "2Gi"},
					}},
			}},
			Post: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:       "ipi-teardown",
					From:     "installer",
					Commands: "openshift-cluster destroy",
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "1000m"},
						Limits:   api.ResourceList{"memory": "2Gi"},
					}},
			}},
		},
		expectedRes: api.MultiStageTestConfigurationLiteral{
			ClusterProfile:     api.ClusterProfileAWS,
			AllowSkipOnSuccess: &yes,
			Pre: []api.LiteralTestStep{{
				As:       "ipi-install",
				From:     "installer",
				Commands: "openshift-cluster install",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Test: []api.LiteralTestStep{{
				As:       "e2e",
				From:     "my-image",
				Commands: "make custom-e2e",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Post: []api.LiteralTestStep{{
				As:       "ipi-teardown",
				From:     "installer",
				Commands: "openshift-cluster destroy",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
		},
	}, {
		name: "Test with reference",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Test: []api.TestStep{{
				Reference: &reference1,
			}},
		},
		stepMap: ReferenceByName{
			reference1: {
				As:       "generic-unit-test",
				From:     "my-image",
				Commands: "make test/unit",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			},
		},
		expectedRes: api.MultiStageTestConfigurationLiteral{
			ClusterProfile: api.ClusterProfileAWS,
			Test: []api.LiteralTestStep{{
				As:       "generic-unit-test",
				From:     "my-image",
				Commands: "make test/unit",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
		},
	}, {
		name: "Test with broken reference",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Test: []api.TestStep{{
				Reference: &reference1,
			}},
		},
		stepMap: ReferenceByName{
			"generic-unit-test-2": {
				As:       "generic-unit-test-2",
				From:     "my-image",
				Commands: "make test/unit",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			},
		},
		expectedRes: api.MultiStageTestConfigurationLiteral{},
		expectedErr: errors.New("test: invalid step reference: generic-unit-test"),
	}, {
		name: "Test with chain and reference",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Pre: []api.TestStep{{
				Chain: &fipsPreChain,
			}},
			Test: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:       "e2e",
					From:     "my-image",
					Commands: "make custom-e2e",
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "1000m"},
						Limits:   api.ResourceList{"memory": "2Gi"},
					}},
			}},
			Post: []api.TestStep{{
				Reference: &teardownRef,
			}},
		},
		chainMap: ChainByName{
			fipsPreChain: {
				Steps: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-install",
						From:     "installer",
						Commands: "openshift-cluster install",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}, {
					LiteralTestStep: &api.LiteralTestStep{
						As:       "enable-fips",
						From:     "fips-enabler",
						Commands: "enable_fips",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
			},
		},
		stepMap: ReferenceByName{
			teardownRef: {
				As:       "ipi-teardown",
				From:     "installer",
				Commands: "openshift-cluster destroy",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				}},
		},
		expectedRes: api.MultiStageTestConfigurationLiteral{
			ClusterProfile: api.ClusterProfileAWS,
			Pre: []api.LiteralTestStep{{
				As:       "ipi-install",
				From:     "installer",
				Commands: "openshift-cluster install",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}, {
				As:       "enable-fips",
				From:     "fips-enabler",
				Commands: "enable_fips",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Test: []api.LiteralTestStep{{
				As:       "e2e",
				From:     "my-image",
				Commands: "make custom-e2e",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Post: []api.LiteralTestStep{{
				As:       "ipi-teardown",
				From:     "installer",
				Commands: "openshift-cluster destroy",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
		},
	}, {
		name: "Test with broken chain",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Test: []api.TestStep{{
				Reference: &fipsPreChain,
			}},
		},
		chainMap: ChainByName{
			"broken": {
				Steps: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "generic-unit-test-2",
						From:     "my-image",
						Commands: "make test/unit",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
			},
		},
		expectedRes: api.MultiStageTestConfigurationLiteral{},
		expectedErr: errors.New("test: invalid step reference: install-fips"),
	}, {
		name: "Test with chain and reference, invalid parameter",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Pre:            []api.TestStep{{Chain: &fipsPreChain}},
		},
		chainMap: ChainByName{
			fipsPreChain: {
				Steps: []api.TestStep{{Reference: &reference1}},
				Environment: []api.StepParameter{
					{Name: nonExistentEnv, Default: &nonExistentEnv},
				},
			},
		},
		stepMap: ReferenceByName{
			reference1: {
				As:       reference1,
				From:     "from",
				Commands: "commands",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			},
		},
		expectedErr:           errors.New(`test: install-fips: no step declares parameter "NON_EXISTENT"`),
		expectedValidationErr: errors.New(`install-fips: no step declares parameter "NON_EXISTENT"`),
	}, {
		name: "Test with nested chains",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Pre: []api.TestStep{{
				Chain: &nestedChains,
			}},
		},
		chainMap: ChainByName{
			nestedChains: {
				Steps: []api.TestStep{{
					Chain: &chainInstall,
				}, {
					LiteralTestStep: &api.LiteralTestStep{
						As:       "enable-fips",
						From:     "fips-enabler",
						Commands: "enable_fips",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
			},
			chainInstall: {
				Steps: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-lease",
						From:     "installer",
						Commands: "lease",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}, {
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-setup",
						From:     "installer",
						Commands: "openshift-cluster install",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
			},
		},
		expectedRes: api.MultiStageTestConfigurationLiteral{
			ClusterProfile: api.ClusterProfileAWS,
			Pre: []api.LiteralTestStep{{
				As:       "ipi-lease",
				From:     "installer",
				Commands: "lease",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}, {
				As:       "ipi-setup",
				From:     "installer",
				Commands: "openshift-cluster install",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}, {
				As:       "enable-fips",
				From:     "fips-enabler",
				Commands: "enable_fips",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				}},
			},
		},
	}, {
		name: "Test with duplicate names after unrolling chains",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Pre: []api.TestStep{{
				Chain: &nestedChains,
			}},
		},
		chainMap: ChainByName{
			nestedChains: {
				Steps: []api.TestStep{{
					Chain: &chainInstall,
				}, {
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-setup",
						From:     "installer",
						Commands: "openshift-cluster install",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
			},
			chainInstall: {
				Steps: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-lease",
						From:     "installer",
						Commands: "lease",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}, {
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-setup",
						From:     "installer",
						Commands: "openshift-cluster install",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
			},
		},
		expectedRes:           api.MultiStageTestConfigurationLiteral{},
		expectedErr:           errors.New("test: nested-chains: duplicate name: ipi-setup"),
		expectedValidationErr: errors.New("nested-chains: duplicate name: ipi-setup"),
	}, {
		name: "Full AWS Workflow",
		config: api.MultiStageTestConfiguration{
			Workflow: &awsWorkflow,
		},
		chainMap: ChainByName{
			fipsPreChain: {
				Steps: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-install",
						From:     "installer",
						Commands: "openshift-cluster install",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}, {
					LiteralTestStep: &api.LiteralTestStep{
						As:       "enable-fips",
						From:     "fips-enabler",
						Commands: "enable_fips",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
			},
		},
		stepMap: ReferenceByName{
			teardownRef: {
				As:       "ipi-teardown",
				From:     "installer",
				Commands: "openshift-cluster destroy",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				}},
		},
		workflowMap: WorkflowByName{
			awsWorkflow: {
				ClusterProfile: api.ClusterProfileAWS,
				Pre: []api.TestStep{{
					Chain: &fipsPreChain,
				}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "e2e",
						From:     "my-image",
						Commands: "make custom-e2e",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
				Post: []api.TestStep{{
					Reference: &teardownRef,
				}},
			},
		},
		expectedRes: api.MultiStageTestConfigurationLiteral{
			ClusterProfile: api.ClusterProfileAWS,
			Pre: []api.LiteralTestStep{{
				As:       "ipi-install",
				From:     "installer",
				Commands: "openshift-cluster install",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				}}, {
				As:       "enable-fips",
				From:     "fips-enabler",
				Commands: "enable_fips",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				}},
			},
			Test: []api.LiteralTestStep{{
				As:       "e2e",
				From:     "my-image",
				Commands: "make custom-e2e",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Post: []api.LiteralTestStep{{
				As:       "ipi-teardown",
				From:     "installer",
				Commands: "openshift-cluster destroy",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
		},
	}, {
		name: "Workflow with Test and ClusterProfile overridden",
		config: api.MultiStageTestConfiguration{
			Workflow:       &awsWorkflow,
			ClusterProfile: api.ClusterProfileAzure4,
			Test: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:       "custom-e2e",
					From:     "test-image",
					Commands: "make custom-e2e-2",
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "1000m"},
						Limits:   api.ResourceList{"memory": "2Gi"},
					}},
			}},
		},
		workflowMap: WorkflowByName{
			awsWorkflow: {
				ClusterProfile: api.ClusterProfileAWS,
				Pre: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-install",
						From:     "installer",
						Commands: "openshift-cluster install",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "e2e",
						From:     "my-image",
						Commands: "make custom-e2e",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
				Post: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-teardown",
						From:     "installer",
						Commands: "openshift-cluster destroy",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						}},
				}},
			},
		},
		expectedRes: api.MultiStageTestConfigurationLiteral{
			ClusterProfile: api.ClusterProfileAzure4,
			Pre: []api.LiteralTestStep{{
				As:       "ipi-install",
				From:     "installer",
				Commands: "openshift-cluster install",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Test: []api.LiteralTestStep{{
				As:       "custom-e2e",
				From:     "test-image",
				Commands: "make custom-e2e-2",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
			Post: []api.LiteralTestStep{{
				As:       "ipi-teardown",
				From:     "installer",
				Commands: "openshift-cluster destroy",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			}},
		},
	}, {
		name: "Workflow with invalid parameter",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Workflow:       &awsWorkflow,
		},
		workflowMap: WorkflowByName{
			awsWorkflow: {
				ClusterProfile: api.ClusterProfileAWS,
				Pre: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "ipi-install",
						From:     "installer",
						Commands: "openshift-cluster install",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						},
						Environment: []api.StepParameter{
							{Name: "STEP_ENV", Default: &stepEnv},
						}},
				}},
				Environment: api.TestEnvironment{
					"NOT_THE_STEP_ENV": "NOT_THE_STEP_ENV",
				},
			},
		},
		expectedErr:           errors.New(`test: ipi-aws: no step declares parameter "NOT_THE_STEP_ENV"`),
		expectedValidationErr: errors.New(`ipi-aws: no step declares parameter "NOT_THE_STEP_ENV"`),
	}} {
		t.Run(testCase.name, func(t *testing.T) {
			err := Validate(testCase.stepMap, testCase.chainMap, testCase.workflowMap)
			if !reflect.DeepEqual(err, utilerrors.NewAggregate([]error{testCase.expectedValidationErr})) {
				t.Errorf("got incorrect validation error: %s", cmp.Diff(err, testCase.expectedValidationErr))
			}
			ret, err := NewResolver(testCase.stepMap, testCase.chainMap, testCase.workflowMap).Resolve("test", testCase.config)
			if !reflect.DeepEqual(err, utilerrors.NewAggregate([]error{testCase.expectedErr})) {
				t.Errorf("got incorrect error: %s", cmp.Diff(err, testCase.expectedErr))
			}
			if !reflect.DeepEqual(ret, testCase.expectedRes) {
				t.Errorf("got incorrect output: %s", diff.ObjectReflectDiff(ret, testCase.expectedRes))
			}
		})
	}
}

func TestResolveParameters(t *testing.T) {
	workflow := "workflow"
	parent := "parent"
	grandParent := "grand-parent"
	grandGrandParent := "grand-grand-parent"
	invalidEnv := "invalid-env"
	notChanged := "not changed"
	changed := "changed"
	defaultGrandGrand := "grand grand parent"
	defaultGrand := "grand parent"
	defaultParent := "parent"
	defaultNotDeclared := "not declared"
	defaultNotChanged := "not changed"
	defaultStr := "default"
	defaultWorkflow := "workflow"
	defaultTest := "test"
	defaultEmpty := ""
	workflows := WorkflowByName{
		workflow: api.MultiStageTestConfiguration{
			Test:         []api.TestStep{{Chain: &grandGrandParent}},
			Environment:  api.TestEnvironment{"CHANGED": "workflow"},
			Dependencies: api.TestDependencies{"CHANGED": "workflow"},
		},
	}
	chains := ChainByName{
		grandGrandParent: {
			Steps: []api.TestStep{{Chain: &grandParent}},
			Environment: []api.StepParameter{
				{Name: "CHANGED", Default: &defaultGrandGrand},
			},
		},
		grandParent: {
			Steps: []api.TestStep{{Chain: &parent}},
			Environment: []api.StepParameter{
				{Name: "CHANGED", Default: &defaultGrand},
			},
		},
		parent: {
			Steps: []api.TestStep{
				{Reference: &notChanged},
				{Reference: &changed},
			},
			Environment: []api.StepParameter{
				{Name: "CHANGED", Default: &defaultParent},
			},
		},
		invalidEnv: {
			Steps: []api.TestStep{{LiteralTestStep: &api.LiteralTestStep{}}},
			Environment: []api.StepParameter{
				{Name: "NOT_DECLARED", Default: &defaultNotDeclared},
			},
		},
	}
	refs := ReferenceByName{
		notChanged: api.LiteralTestStep{
			As: notChanged,
			Environment: []api.StepParameter{
				{Name: "NOT_CHANGED", Default: &defaultNotChanged},
			},
			Dependencies: []api.StepDependency{
				{Env: "NOT_CHANGED", Name: defaultNotChanged},
			},
		},
		changed: api.LiteralTestStep{
			As:          changed,
			Environment: []api.StepParameter{{Name: "CHANGED"}},
			Dependencies: []api.StepDependency{
				{Env: "CHANGED", Name: defaultNotChanged},
			},
		},
	}
	for _, tc := range []struct {
		name           string
		test           api.MultiStageTestConfiguration
		expectedParams [][]api.StepParameter
		expectedDeps   [][]api.StepDependency
		err            error
	}{{
		name: "leaf, no parameters",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{LiteralTestStep: &api.LiteralTestStep{}}},
		},
		expectedParams: [][]api.StepParameter{nil},
		expectedDeps:   [][]api.StepDependency{nil},
	}, {
		name: "leaf, empty default",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					Environment: []api.StepParameter{
						{Name: "TEST", Default: &defaultEmpty},
					},
				},
			}},
		},
		expectedParams: [][]api.StepParameter{{{
			Name: "TEST", Default: &defaultEmpty,
		}}},
		expectedDeps: [][]api.StepDependency{nil},
	}, {
		name: "leaf, parameters, deps",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					Environment: []api.StepParameter{
						{Name: "TEST", Default: &defaultStr},
					},
					Dependencies: []api.StepDependency{
						{Name: "test", Env: "WHOA"},
					},
				},
			}},
		},
		expectedParams: [][]api.StepParameter{{{
			Name: "TEST", Default: &defaultStr,
		}}},
		expectedDeps: [][]api.StepDependency{{{
			Name: "test", Env: "WHOA",
		}}},
	}, {
		name: "chain propagates to sub-steps",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{Chain: &parent}},
		},
		expectedParams: [][]api.StepParameter{
			{{Name: "NOT_CHANGED", Default: &defaultNotChanged}},
			{{Name: "CHANGED", Default: &defaultParent}},
		},
		expectedDeps: [][]api.StepDependency{
			{{Env: "NOT_CHANGED", Name: defaultNotChanged}},
			{{Env: "CHANGED", Name: defaultNotChanged}},
		},
	}, {
		name: "change propagates to sub-chains",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{Chain: &grandGrandParent}},
		},
		expectedParams: [][]api.StepParameter{
			{{Name: "NOT_CHANGED", Default: &defaultNotChanged}},
			{{Name: "CHANGED", Default: &defaultGrandGrand}},
		},
		expectedDeps: [][]api.StepDependency{
			{{Env: "NOT_CHANGED", Name: defaultNotChanged}},
			{{Env: "CHANGED", Name: defaultNotChanged}},
		},
	}, {
		name: "workflow parameter and dep",
		test: api.MultiStageTestConfiguration{Workflow: &workflow},
		expectedParams: [][]api.StepParameter{
			{{Name: "NOT_CHANGED", Default: &defaultNotChanged}},
			{{Name: "CHANGED", Default: &defaultWorkflow}},
		},
		expectedDeps: [][]api.StepDependency{
			{{Env: "NOT_CHANGED", Name: defaultNotChanged}},
			{{Env: "CHANGED", Name: defaultWorkflow}},
		},
	}, {
		name: "test parameter and dep",
		test: api.MultiStageTestConfiguration{
			Test:         []api.TestStep{{Chain: &grandGrandParent}},
			Environment:  api.TestEnvironment{"CHANGED": "test"},
			Dependencies: api.TestDependencies{"CHANGED": "test"},
		},
		expectedParams: [][]api.StepParameter{
			{{Name: "NOT_CHANGED", Default: &defaultNotChanged}},
			{{Name: "CHANGED", Default: &defaultTest}},
		},
		expectedDeps: [][]api.StepDependency{
			{{Env: "NOT_CHANGED", Name: defaultNotChanged}},
			{{Env: "CHANGED", Name: defaultTest}},
		},
	}, {
		name: "invalid chain parameter",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{Chain: &invalidEnv}},
		},
		err: errors.New(`test: invalid-env: no step declares parameter "NOT_DECLARED"`),
	}, {
		name: "invalid test parameter",
		test: api.MultiStageTestConfiguration{
			Test:        []api.TestStep{{Reference: &notChanged}},
			Environment: api.TestEnvironment{"NOT_DECLARED": "not declared"},
		},
		err: errors.New(`test: no step declares parameter "NOT_DECLARED"`),
	}, {
		name: "invalid test dep",
		test: api.MultiStageTestConfiguration{
			Test:         []api.TestStep{{Reference: &notChanged}},
			Dependencies: api.TestDependencies{"NOT_DECLARED": "not declared"},
		},
		err: errors.New(`test: no step declares dependency "NOT_DECLARED"`),
	}, {
		name: "unresolved test",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:          "step",
					Environment: []api.StepParameter{{Name: "UNRESOLVED"}},
				},
			}},
		},
		err: errors.New("test: step: unresolved parameter: UNRESOLVED"),
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret, err := NewResolver(refs, chains, workflows).Resolve("test", tc.test)
			if tc.err != nil {
				if err == nil {
					t.Fatal("unexpected success")
				}
				if diff := cmp.Diff(err.Error(), tc.err.Error()); diff != "" {
					t.Fatal(diff)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				var params [][]api.StepParameter
				for _, s := range append(ret.Pre, append(ret.Test, ret.Post...)...) {
					params = append(params, s.Environment)
				}
				if diff := cmp.Diff(params, tc.expectedParams); diff != "" {
					t.Error(diff)
				}
				var deps [][]api.StepDependency
				for _, s := range append(ret.Pre, append(ret.Test, ret.Post...)...) {
					deps = append(deps, s.Dependencies)
				}
				if diff := cmp.Diff(deps, tc.expectedDeps); diff != "" {
					t.Error(diff)
				}
			}
		})
	}
}
