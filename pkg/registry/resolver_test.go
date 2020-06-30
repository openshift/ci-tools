package registry

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/api"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestResolve(t *testing.T) {
	reference1 := "generic-unit-test"
	teardownRef := "teardown"
	fipsPreChain := "install-fips"
	nestedChains := "nested-chains"
	chainInstall := "install-chain"
	awsWorkflow := "ipi-aws"
	for _, testCase := range []struct {
		name        string
		config      api.MultiStageTestConfiguration
		stepMap     ReferenceByName
		chainMap    ChainByName
		workflowMap WorkflowByName
		expectedRes api.MultiStageTestConfigurationLiteral
		expectedErr error
	}{{
		// This is a full config that should not change (other than struct) when passed to the Resolver
		name: "Full AWS IPI",
		config: api.MultiStageTestConfiguration{
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
		expectedRes: api.MultiStageTestConfigurationLiteral{},
		expectedErr: errors.New("test: nested-chains: duplicate name: ipi-setup"),
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
	}} {
		t.Run(testCase.name, func(t *testing.T) {
			ret, err := NewResolver(testCase.stepMap, testCase.chainMap, testCase.workflowMap).Resolve("test", testCase.config)
			if testCase.expectedErr == nil {
				if err != nil {
					t.Fatalf("%s: expected no error but got: %s", testCase.name, err)
				}
			} else {
				if err == nil {
					t.Fatalf("%s: expected error but got none", testCase.name)
				}
				if testCase.expectedErr.Error() != err.Error() {
					t.Fatalf("%s: got incorrect error: %s", testCase.name, diff.ObjectReflectDiff(testCase.expectedErr, err))
				}
			}
			if !reflect.DeepEqual(ret, testCase.expectedRes) {
				t.Errorf("%s: got incorrect output: %s", testCase.name, diff.ObjectReflectDiff(ret, testCase.expectedRes))
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
			Test:        []api.TestStep{{Chain: &grandGrandParent}},
			Environment: api.TestEnvironment{"CHANGED": "workflow"},
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
		},
		changed: api.LiteralTestStep{
			As:          changed,
			Environment: []api.StepParameter{{Name: "CHANGED"}},
		},
	}
	for _, tc := range []struct {
		name     string
		test     api.MultiStageTestConfiguration
		expected [][]api.StepParameter
		err      error
	}{{
		name: "leaf, no parameters",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{LiteralTestStep: &api.LiteralTestStep{}}},
		},
		expected: [][]api.StepParameter{nil},
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
		expected: [][]api.StepParameter{{{
			Name: "TEST", Default: &defaultEmpty,
		}}},
	}, {
		name: "leaf, parameters",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					Environment: []api.StepParameter{
						{Name: "TEST", Default: &defaultStr},
					},
				},
			}},
		},
		expected: [][]api.StepParameter{{{
			Name: "TEST", Default: &defaultStr,
		}}},
	}, {
		name: "chain propagates to sub-steps",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{Chain: &parent}},
		},
		expected: [][]api.StepParameter{
			{{Name: "NOT_CHANGED", Default: &defaultNotChanged}},
			{{Name: "CHANGED", Default: &defaultParent}},
		},
	}, {
		name: "change propagates to sub-chains",
		test: api.MultiStageTestConfiguration{
			Test: []api.TestStep{{Chain: &grandGrandParent}},
		},
		expected: [][]api.StepParameter{
			{{Name: "NOT_CHANGED", Default: &defaultNotChanged}},
			{{Name: "CHANGED", Default: &defaultGrandGrand}},
		},
	}, {
		name: "workflow parameter",
		test: api.MultiStageTestConfiguration{Workflow: &workflow},
		expected: [][]api.StepParameter{
			{{Name: "NOT_CHANGED", Default: &defaultNotChanged}},
			{{Name: "CHANGED", Default: &defaultWorkflow}},
		},
	}, {
		name: "test parameter",
		test: api.MultiStageTestConfiguration{
			Test:        []api.TestStep{{Chain: &grandGrandParent}},
			Environment: api.TestEnvironment{"CHANGED": "test"},
		},
		expected: [][]api.StepParameter{
			{{Name: "NOT_CHANGED", Default: &defaultNotChanged}},
			{{Name: "CHANGED", Default: &defaultTest}},
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
				if diff := cmp.Diff(params, tc.expected); diff != "" {
					t.Error(diff)
				}
			}
		})
	}
}
