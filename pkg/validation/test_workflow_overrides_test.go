package validation

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestValidateWorkflowOverrides(t *testing.T) {
	truePtr := func(b bool) *bool { return &b }

	// Define test workflows with pre and post steps
	workflowWithPrePost := "workflow-with-pre-post"
	workflowWithOnlyPre := "workflow-with-only-pre"
	workflowWithOnlyPost := "workflow-with-only-post"
	workflowEmpty := "workflow-empty"

	workflows := map[string]api.MultiStageTestConfiguration{
		workflowWithPrePost: {
			Pre: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:       "pre-setup",
					From:     "cli",
					Commands: "setup commands",
				},
			}},
			Post: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:       "post-cleanup",
					From:     "cli",
					Commands: "cleanup commands",
				},
			}},
		},
		workflowWithOnlyPre: {
			Pre: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:       "pre-setup",
					From:     "cli",
					Commands: "setup commands",
				},
			}},
		},
		workflowWithOnlyPost: {
			Post: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{
					As:       "post-cleanup",
					From:     "cli",
					Commands: "cleanup commands",
				},
			}},
		},
		workflowEmpty: {},
	}

	validator := NewValidatorWithWorkflows(nil, nil, workflows)

	testCases := []struct {
		name             string
		config           api.MultiStageTestConfiguration
		expectError      bool
		expectedErrorMsg string
	}{
		{
			name: "no override - should pass",
			config: api.MultiStageTestConfiguration{
				Workflow: &workflowWithPrePost,
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
			},
			expectError: false,
		},
		{
			name: "override pre without flag - should fail",
			config: api.MultiStageTestConfiguration{
				Workflow: &workflowWithPrePost,
				Pre: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-pre",
						From:     "cli",
						Commands: "my pre commands",
					},
				}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
			},
			expectError:      true,
			expectedErrorMsg: "test configuration overrides pre steps from workflow \"workflow-with-pre-post\" but does not have 'allow_pre_post_step_overrides: true' set",
		},
		{
			name: "override post without flag - should fail",
			config: api.MultiStageTestConfiguration{
				Workflow: &workflowWithPrePost,
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
				Post: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-post",
						From:     "cli",
						Commands: "my post commands",
					},
				}},
			},
			expectError:      true,
			expectedErrorMsg: "test configuration overrides post steps from workflow \"workflow-with-pre-post\" but does not have 'allow_pre_post_step_overrides: true' set",
		},
		{
			name: "override both pre and post without flag - should fail",
			config: api.MultiStageTestConfiguration{
				Workflow: &workflowWithPrePost,
				Pre: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-pre",
						From:     "cli",
						Commands: "my pre commands",
					},
				}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
				Post: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-post",
						From:     "cli",
						Commands: "my post commands",
					},
				}},
			},
			expectError:      true,
			expectedErrorMsg: "test configuration overrides pre and post steps from workflow \"workflow-with-pre-post\" but does not have 'allow_pre_post_step_overrides: true' set",
		},
		{
			name: "override pre with flag - should pass",
			config: api.MultiStageTestConfiguration{
				Workflow:                  &workflowWithPrePost,
				AllowPrePostStepOverrides: truePtr(true),
				Pre: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-pre",
						From:     "cli",
						Commands: "my pre commands",
					},
				}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
			},
			expectError: false,
		},
		{
			name: "override post with flag - should pass",
			config: api.MultiStageTestConfiguration{
				Workflow:                  &workflowWithPrePost,
				AllowPrePostStepOverrides: truePtr(true),
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
				Post: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-post",
						From:     "cli",
						Commands: "my post commands",
					},
				}},
			},
			expectError: false,
		},
		{
			name: "override both pre and post with flag - should pass",
			config: api.MultiStageTestConfiguration{
				Workflow:                  &workflowWithPrePost,
				AllowPrePostStepOverrides: truePtr(true),
				Pre: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-pre",
						From:     "cli",
						Commands: "my pre commands",
					},
				}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
				Post: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-post",
						From:     "cli",
						Commands: "my post commands",
					},
				}},
			},
			expectError: false,
		},
		{
			name: "override with flag set to false - should fail",
			config: api.MultiStageTestConfiguration{
				Workflow:                  &workflowWithPrePost,
				AllowPrePostStepOverrides: truePtr(false),
				Pre: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-pre",
						From:     "cli",
						Commands: "my pre commands",
					},
				}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
			},
			expectError:      true,
			expectedErrorMsg: "test configuration overrides pre steps from workflow \"workflow-with-pre-post\" but does not have 'allow_pre_post_step_overrides: true' set",
		},
		{
			name: "empty workflow - no validation needed",
			config: api.MultiStageTestConfiguration{
				Workflow: &workflowEmpty,
				Pre: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-pre",
						From:     "cli",
						Commands: "my pre commands",
					},
				}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
				Post: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-post",
						From:     "cli",
						Commands: "my post commands",
					},
				}},
			},
			expectError: false,
		},
		{
			name: "override only pre from workflow with only pre - should fail",
			config: api.MultiStageTestConfiguration{
				Workflow: &workflowWithOnlyPre,
				Pre: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-pre",
						From:     "cli",
						Commands: "my pre commands",
					},
				}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
			},
			expectError:      true,
			expectedErrorMsg: "test configuration overrides pre steps from workflow \"workflow-with-only-pre\" but does not have 'allow_pre_post_step_overrides: true' set",
		},
		{
			name: "override only post from workflow with only post - should fail",
			config: api.MultiStageTestConfiguration{
				Workflow: &workflowWithOnlyPost,
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test-step",
						From:     "cli",
						Commands: "test commands",
					},
				}},
				Post: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "my-post",
						From:     "cli",
						Commands: "my post commands",
					},
				}},
			},
			expectError:      true,
			expectedErrorMsg: "test configuration overrides post steps from workflow \"workflow-with-only-post\" but does not have 'allow_pre_post_step_overrides: true' set",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test the validation by creating a test configuration with the MultiStageTestConfiguration
			testConfig := api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{
					{
						As:                          "test-name",
						MultiStageTestConfiguration: &tc.config,
					},
				},
			}

			errs := validator.validateTestStepConfiguration(NewConfigContext(), "tests", testConfig.Tests, nil, nil, sets.New[string](), sets.New[string](), false)

			if tc.expectError {
				if len(errs) == 0 {
					t.Fatalf("Expected error but got none")
				}
				found := false
				for _, err := range errs {
					if err != nil && strings.Contains(err.Error(), tc.expectedErrorMsg) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected error message to contain %q, got errors: %v", tc.expectedErrorMsg, errs)
				}
			} else {
				// Filter out non-workflow validation errors and only look for workflow override errors
				var workflowErrs []string
				for _, err := range errs {
					if err != nil && (strings.Contains(err.Error(), "allow_pre_post_step_overrides") || strings.Contains(err.Error(), "overrides") && strings.Contains(err.Error(), "workflow")) {
						workflowErrs = append(workflowErrs, err.Error())
					}
				}
				if len(workflowErrs) > 0 {
					t.Fatalf("Expected no workflow override error but got: %v", workflowErrs)
				}
			}
		})
	}
}
