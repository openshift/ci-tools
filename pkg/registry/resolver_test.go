package registry

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/diff"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
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
		observerMap           ObserverByName
		expectedRes           api.MultiStageTestConfigurationLiteral
		expectedErr           error
		expectedValidationErr error
	}{{
		// This is a full config that should not change (other than struct) when passed to the Resolver
		name: "Full AWS IPI",
		config: api.MultiStageTestConfiguration{
			ClusterProfile:           api.ClusterProfileAWS,
			AllowSkipOnSuccess:       &yes,
			AllowBestEffortPostSteps: &yes,
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
					},
				},
			}},
		},
		expectedRes: api.MultiStageTestConfigurationLiteral{
			ClusterProfile:           api.ClusterProfileAWS,
			AllowSkipOnSuccess:       &yes,
			AllowBestEffortPostSteps: &yes,
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
		name: "Test with observers",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Test: []api.TestStep{{
				Reference: &reference1,
			}},
			Observers: &api.Observers{
				Enable:  []string{"yes"},
				Disable: []string{"no"},
			},
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
				Observers: []string{"no", "other"},
			},
		},
		observerMap: map[string]api.Observer{
			"yes": {
				Name:     "yes",
				From:     "src",
				Commands: "exit",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			},
			"no": {
				Name:     "no",
				From:     "src",
				Commands: "exit",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				},
			},
			"other": {
				Name:     "other",
				From:     "src",
				Commands: "exit",
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
				Observers: []string{"no", "other"},
			}},
			Observers: []api.Observer{
				{
					Name:     "other",
					From:     "src",
					Commands: "exit",
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "1000m"},
						Limits:   api.ResourceList{"memory": "2Gi"},
					},
				}, {
					Name:     "yes",
					From:     "src",
					Commands: "exit",
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "1000m"},
						Limits:   api.ResourceList{"memory": "2Gi"},
					},
				},
			},
		},
	}, {
		name: "Test with broken observer",
		config: api.MultiStageTestConfiguration{
			ClusterProfile: api.ClusterProfileAWS,
			Test: []api.TestStep{{
				Reference: &reference1,
			}},
			Observers: &api.Observers{
				Enable:  []string{"yes"},
				Disable: []string{"no"},
			},
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
				Observers: []string{"no"},
			},
		},
		observerMap: map[string]api.Observer{},
		expectedRes: api.MultiStageTestConfigurationLiteral{},
		expectedErr: errors.New("observer \"yes\" is referenced but no such observer is configured"),
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
		expectedErr: errors.New("test/test: invalid step reference: generic-unit-test"),
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
		expectedErr: errors.New("test/test: invalid step reference: install-fips"),
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
		expectedErr:           errors.New(`test/test: chain/install-fips: parameter "NON_EXISTENT" is overridden in [chain/install-fips] but not declared in any step`),
		expectedValidationErr: errors.New(`chain/install-fips: parameter "NON_EXISTENT" is overridden in [chain/install-fips] but not declared in any step`),
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
		expectedErr:           errors.New("test/test: chain/nested-chains: duplicate name: ipi-setup"),
		expectedValidationErr: errors.New("chain/nested-chains: duplicate name: ipi-setup"),
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
		expectedErr:           errors.New(`test/test: workflow/ipi-aws: parameter "NOT_THE_STEP_ENV" is overridden in [test/test] but not declared in any step`),
		expectedValidationErr: errors.New(`workflow/ipi-aws: parameter "NOT_THE_STEP_ENV" is overridden in [workflow/ipi-aws] but not declared in any step`),
	}} {
		t.Run(testCase.name, func(t *testing.T) {
			err := Validate(testCase.stepMap, testCase.chainMap, testCase.workflowMap, testCase.observerMap)
			if !reflect.DeepEqual(err, utilerrors.NewAggregate([]error{testCase.expectedValidationErr})) {
				t.Errorf("got incorrect validation error: %s", cmp.Diff(err, testCase.expectedValidationErr))
			}
			ret, err := NewResolver(testCase.stepMap, testCase.chainMap, testCase.workflowMap, testCase.observerMap).Resolve("test", testCase.config)
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
	testMergeWorkflow := "test merge workflow"
	parent := "parent"
	grandParent := "grand-parent"
	grandGrandParent := "grand-grand-parent"
	invalidEnv := "invalid-env"
	notChanged := "not changed"
	changed := "changed"
	mergeRef := "merge ref"
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
		testMergeWorkflow: api.MultiStageTestConfiguration{
			Test: []api.TestStep{
				{Chain: &grandGrandParent},
				{Reference: &mergeRef},
			},
			Environment: api.TestEnvironment{
				"CHANGED":   "workflow",
				"FROM_TEST": "from workflow, will be overwritten",
			},
			Dependencies: api.TestDependencies{
				"CHANGED":   "workflow",
				"FROM_TEST": "from_workflow, will be overwritten",
			},
			DependencyOverrides: api.DependencyOverrides{
				"FROM_WORKFLOW": defaultWorkflow,
			},
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
		mergeRef: api.LiteralTestStep{
			As:          mergeRef,
			Environment: []api.StepParameter{{Name: "FROM_TEST"}},
			Dependencies: []api.StepDependency{
				{Env: "FROM_TEST", Name: "from test, will be overwritten"},
			},
		},
	}
	observers := ObserverByName{}
	for _, tc := range []struct {
		name                 string
		test                 api.MultiStageTestConfiguration
		expectedParams       [][]api.StepParameter
		expectedDeps         [][]api.StepDependency
		expectedDepOverrides api.DependencyOverrides
		err                  error
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
		name: "test and workflow are merged",
		test: api.MultiStageTestConfiguration{
			Workflow:            &testMergeWorkflow,
			Environment:         api.TestEnvironment{"FROM_TEST": defaultTest},
			Dependencies:        api.TestDependencies{"FROM_TEST": defaultTest},
			DependencyOverrides: api.DependencyOverrides{"ADDED": defaultTest},
		},
		expectedParams: [][]api.StepParameter{
			{{Name: "NOT_CHANGED", Default: &defaultNotChanged}},
			{{Name: "CHANGED", Default: &defaultWorkflow}},
			{{Name: "FROM_TEST", Default: &defaultTest}},
		},
		expectedDeps: [][]api.StepDependency{
			{{Env: "NOT_CHANGED", Name: defaultNotChanged}},
			{{Env: "CHANGED", Name: defaultWorkflow}},
			{{Env: "FROM_TEST", Name: defaultTest}},
		},
		expectedDepOverrides: api.DependencyOverrides{
			"FROM_WORKFLOW": defaultWorkflow,
			"ADDED":         defaultTest,
		},
	}, {
		name: "invalid chain parameter",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{Chain: &invalidEnv}},
		},
		err: errors.New(`test/test: chain/invalid-env: parameter "NOT_DECLARED" is overridden in [chain/invalid-env] but not declared in any step`),
	}, {
		name: "invalid test parameter",
		test: api.MultiStageTestConfiguration{
			Test:        []api.TestStep{{Reference: &notChanged}},
			Environment: api.TestEnvironment{"NOT_DECLARED": "not declared"},
		},
		err: errors.New(`test/test: parameter "NOT_DECLARED" is overridden in [test/test] but not declared in any step`),
	}, {
		name: "invalid test dep",
		test: api.MultiStageTestConfiguration{
			Test:         []api.TestStep{{Reference: &notChanged}},
			Dependencies: api.TestDependencies{"NOT_DECLARED": "not declared"},
		},
		err: errors.New(`test/test: dependency "NOT_DECLARED" is overridden in [test/test] but not declared in any step`),
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
		err: errors.New("test/test: step/step: unresolved parameter: UNRESOLVED"),
	}, {
		name: "unresolved workflow override is not an error",
		test: api.MultiStageTestConfiguration{
			Workflow: &workflow,
			Test: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{As: "step"},
			}},
		},
		expectedParams: [][]api.StepParameter{nil},
		expectedDeps:   [][]api.StepDependency{nil},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret, err := NewResolver(refs, chains, workflows, observers).Resolve("test", tc.test)
			var params [][]api.StepParameter
			var deps [][]api.StepDependency
			for _, l := range [][]api.LiteralTestStep{ret.Pre, ret.Test, ret.Post} {
				for _, s := range l {
					params = append(params, s.Environment)
					deps = append(deps, s.Dependencies)
				}
			}
			testhelper.Diff(t, "error", err, tc.err, testhelper.EquateErrorMessage)
			testhelper.Diff(t, "parameters", params, tc.expectedParams)
			testhelper.Diff(t, "dependencies", deps, tc.expectedDeps)
			testhelper.Diff(t, "dependency overrides", ret.DependencyOverrides, tc.expectedDepOverrides)
		})
	}
}

func TestResolveLeases(t *testing.T) {
	ref0 := "ref0"
	chain0 := "chain0"
	workflow0 := "workflow0"
	refs := ReferenceByName{
		ref0: {Leases: []api.StepLease{{ResourceType: "from_ref"}}},
	}
	chains := ChainByName{
		chain0: {
			Leases: []api.StepLease{{ResourceType: "from_chain"}},
		},
	}
	workflows := WorkflowByName{
		workflow0: {
			Leases: []api.StepLease{
				{ResourceType: "from_workflow", Env: "FROM_WORKFLOW"},
			},
		},
	}
	for _, tc := range []struct {
		name        string
		test        api.MultiStageTestConfiguration
		expected    []api.StepLease
		expectedErr error
	}{{
		name: "listed directly in the test",
		test: api.MultiStageTestConfiguration{
			Leases: []api.StepLease{{ResourceType: "from_test"}},
		},
		expected: []api.StepLease{{ResourceType: "from_test"}},
	}, {
		name: "from workflow",
		test: api.MultiStageTestConfiguration{Workflow: &workflow0},
		expected: []api.StepLease{
			{ResourceType: "from_workflow", Env: "FROM_WORKFLOW"},
		},
	}, {
		name: "test merged with workflow",
		test: api.MultiStageTestConfiguration{
			Workflow: &workflow0,
			Leases: []api.StepLease{
				{ResourceType: "from_step", Env: "FROM_STEP"},
			},
		},
		expected: []api.StepLease{
			{ResourceType: "from_workflow", Env: "FROM_WORKFLOW"},
			{ResourceType: "from_step", Env: "FROM_STEP"},
		},
	}, {
		name: "test cannot change workflow's variable name",
		test: api.MultiStageTestConfiguration{
			Workflow: &workflow0,
			Leases: []api.StepLease{
				{ResourceType: "different_from_workflow", Env: "FROM_WORKFLOW"},
			},
		},
		expectedErr: utilerrors.NewAggregate([]error{
			fmt.Errorf(`cannot override workflow environment variable for lease(s): [FROM_WORKFLOW]`),
		}),
	}, {
		name: "chain is deferred to test execution",
		test: api.MultiStageTestConfiguration{
			Pre: []api.TestStep{{Chain: &chain0}},
		},
	}, {
		name: "reference is deferred to test execution",
		test: api.MultiStageTestConfiguration{
			Pre: []api.TestStep{{Reference: &ref0}},
		},
	}, {
		name: "step is deferred to test execution",
		test: api.MultiStageTestConfiguration{
			Pre: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					Leases: []api.StepLease{{ResourceType: "from_step"}},
				},
			}},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret, err := NewResolver(refs, chains, workflows, ObserverByName{}).Resolve("test", tc.test)
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("unexpected error: %v", diff)
			}
			if diff := cmp.Diff(tc.expected, ret.Leases); diff != "" {
				t.Errorf("unexpected leases: %v", diff)
			}
		})
	}
}

func TestResolveLeasesCopy(t *testing.T) {
	ref := "ref"
	refs := ReferenceByName{
		ref: {As: ref, Leases: []api.StepLease{{}}},
	}
	test := api.MultiStageTestConfiguration{
		Test: []api.TestStep{{Reference: &ref}},
	}
	ret0, err := NewResolver(refs, nil, nil, nil).Resolve("test", test)
	if err != nil {
		t.Fatal(err)
	}
	ret1, err := NewResolver(refs, nil, nil, nil).Resolve("test", test)
	if err != nil {
		t.Fatal(err)
	}
	ret0.Test[0].Leases[0].Count = 42
	leases := []api.StepLease{ret0.Test[0].Leases[0], ret1.Test[0].Leases[0]}
	expected := []api.StepLease{{Count: 42}, {Count: 0}}
	testhelper.Diff(t, "leases", leases, expected)
}
