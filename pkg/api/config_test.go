package api

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestValidateTests(t *testing.T) {
	testInterval := "0 0 0 0 0"
	var validationErrors []error
	var testTestsCases = []struct {
		id            string
		release       *ReleaseTagConfiguration
		tests         []TestStepConfiguration
		expectedValid bool
	}{
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "unit"}}`,
			tests: []TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: true,
		},
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "images"}}`,
			tests: []TestStepConfiguration{
				{
					As:                         "images",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: "No test type",
			tests: []TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
				},
			},
			expectedValid: false,
		},
		{
			id: "Multiple test types",
			tests: []TestStepConfiguration{
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{},
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration{ClusterProfile: ClusterProfileAWSAtomic}},
				},
			},
			expectedValid: false,
		},
		{
			id: "`commands` and `steps`",
			tests: []TestStepConfiguration{
				{
					As:                          "test",
					Commands:                    "commands",
					MultiStageTestConfiguration: &MultiStageTestConfiguration{},
				},
			},
			expectedValid: false,
		},
		{
			id: "container test without `from`",
			tests: []TestStepConfiguration{
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{},
				},
			},
			expectedValid: false,
		},
		{
			id: "test without `commands`",
			tests: []TestStepConfiguration{
				{
					As:                         "test",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: "test valid memory backed volume",
			tests: []TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{
						From: "ignored",
						MemoryBackedVolume: &MemoryBackedVolume{
							Size: "1Gi",
						},
					},
				},
			},
			expectedValid: true,
		},
		{
			id: "test invalid memory backed volume",
			tests: []TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{
						From: "ignored",
						MemoryBackedVolume: &MemoryBackedVolume{
							Size: "1GG", // not valid
						},
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "test with duplicated `as`",
			tests: []TestStepConfiguration{
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
				},
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: "test without `as`",
			tests: []TestStepConfiguration{
				{
					Commands:                   "test",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid cluster profile",
			tests: []TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{},
				},
			},
			expectedValid: false,
		},
		{
			id: "release missing",
			tests: []TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
		},
		{
			id: "release must be origin",
			tests: []TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{},
			expectedValid: true,
		},
		{
			id: "with release",
			tests: []TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: true,
		},
		{
			id: "with invalid policy",
			tests: []TestStepConfiguration{
				{
					As:                "test",
					Commands:          "commands",
					PullRequestPolicy: "Unknown",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: false,
		},
		{
			id: "with valid Always policy",
			tests: []TestStepConfiguration{
				{
					As:                "test",
					Commands:          "commands",
					PullRequestPolicy: "Always",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: true,
		},
		{
			id: "with valid OnRequest policy",
			tests: []TestStepConfiguration{
				{
					As:                "test",
					Commands:          "commands",
					PullRequestPolicy: "OnRequest",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: true,
		},
		{
			id: "with valid RunIfChanged policy",
			tests: []TestStepConfiguration{
				{
					As:                      "test",
					Commands:                "commands",
					PullRequestPolicy:       "RunIfChanged",
					PullRequestRunIfChanged: ".*",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: true,
		},
		{
			id: "with invalid RunIfChanged policy",
			tests: []TestStepConfiguration{
				{
					As:                      "test",
					Commands:                "commands",
					PullRequestPolicy:       "RunIfChanged",
					PullRequestRunIfChanged: "",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: false,
		},
		{
			id: "with invalid regex run_if_changed",
			tests: []TestStepConfiguration{
				{
					As:                      "test",
					Commands:                "commands",
					PullRequestPolicy:       "RunIfChanged",
					PullRequestRunIfChanged: "*",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: false,
		},
		{
			id: "pull request policy should conflict with cron",
			tests: []TestStepConfiguration{
				{
					As:                "test",
					Commands:          "commands",
					PullRequestPolicy: "OnRequest",
					Cron:              &testInterval,
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: false,
		},
		{
			id: "pull request policy should conflict with run_if_changed",
			tests: []TestStepConfiguration{
				{
					As:                      "test",
					Commands:                "commands",
					PullRequestPolicy:       "OnRequest",
					PullRequestRunIfChanged: ".*",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: ClusterTestConfiguration{ClusterProfile: ClusterProfileGCP},
					},
				},
			},
			release:       &ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: false,
		},
		{
			id: "invalid secret mountPath",
			tests: []TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{},
					Secret: &Secret{
						Name:      "secret",
						MountPath: "/path/to/secret:exec",
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid secret name",
			tests: []TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{},
					Secret: &Secret{
						Name:      "secret_test",
						MountPath: "/path/to/secret:exec",
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "valid secret",
			tests: []TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
					Secret: &Secret{
						Name: "secret",
					},
				},
			},
			expectedValid: true,
		},
		{
			id: "valid secret with path",
			tests: []TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
					Secret: &Secret{
						Name:      "secret",
						MountPath: "/path/to/secret",
					},
				},
			},
			expectedValid: true,
		},
		{
			id: "valid secret with invalid path",
			tests: []TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
					Secret: &Secret{
						Name:      "secret",
						MountPath: "path/to/secret",
					},
				},
			},
			expectedValid: false,
		},
	}

	for _, tc := range testTestsCases {
		t.Run(tc.id, func(t *testing.T) {
			if errs := validateTestStepConfiguration("tests", tc.tests, tc.release); len(errs) > 0 && tc.expectedValid {
				validationErrors = append(validationErrors, fmt.Errorf("expected to be valid, got: %v", errs))
			} else if !tc.expectedValid && len(errs) == 0 {
				validationErrors = append(validationErrors, parseValidError(tc.id))
			}
		})
	}

	if validationErrors != nil {
		t.Errorf("Errors: %v", kerrors.NewAggregate(validationErrors))
	}
}

func TestValidateBuildRoot(t *testing.T) {
	var validationErrors []error
	var testBuildRootCases = []struct {
		id                   string
		buildRootImageConfig *BuildRootImageConfiguration
		hasImages            bool
		expectedValid        bool
	}{
		{
			id: "both project_image and image_stream_tag in build_root defined causes error",
			buildRootImageConfig: &BuildRootImageConfiguration{
				ImageStreamTagReference: &ImageStreamTagReference{
					Cluster:   "https://test.org",
					Namespace: "test_namespace",
					Name:      "test_name",
					Tag:       "test",
				},
				ProjectImageBuild: &ProjectDirectoryImageBuildInputs{
					ContextDir:     "/",
					DockerfilePath: "Dockerfile.test",
				},
			},
			expectedValid: false,
		},
		{
			id:                   "build root without any content causes an error",
			buildRootImageConfig: &BuildRootImageConfiguration{},
			expectedValid:        false,
		},
		{
			id:                   "nil build root is allowed when no images",
			buildRootImageConfig: nil,
			hasImages:            false,
			expectedValid:        true,
		},
		{
			id:                   "nil build root is not allowed when images defined",
			buildRootImageConfig: nil,
			hasImages:            true,
			expectedValid:        false,
		},
	}

	for _, tc := range testBuildRootCases {
		if errs := validateBuildRootImageConfiguration("build_root", tc.buildRootImageConfig, tc.hasImages); len(errs) > 0 && tc.expectedValid {
			validationErrors = append(validationErrors, fmt.Errorf("%q expected to be valid, got: %v", tc.id, errs))
		} else if !tc.expectedValid && len(errs) == 0 {
			validationErrors = append(validationErrors, parseValidError(tc.id))
		}
	}
	if validationErrors != nil {
		t.Errorf("Errors: %v", kerrors.NewAggregate(validationErrors))
	}
}

func TestValidateBaseImages(t *testing.T) {
	var validationErrors []error
	var testBaseImagesCases = []struct {
		id            string
		baseImages    map[string]ImageStreamTagReference
		expectedValid bool
	}{
		{
			id: "base images",
			baseImages: map[string]ImageStreamTagReference{"test": {Cluster: "test"},
				"test2": {Tag: "test2"}, "test3": {Cluster: "test3"},
			},
			expectedValid: false,
		},
	}
	for _, tc := range testBaseImagesCases {
		if errs := validateImageStreamTagReferenceMap("base_images", tc.baseImages); len(errs) > 0 && tc.expectedValid {
			validationErrors = append(validationErrors, fmt.Errorf("%q expected to be valid, got: %v", tc.id, errs))
		} else if !tc.expectedValid && len(errs) == 0 {
			validationErrors = append(validationErrors, parseValidError(tc.id))
		}
	}
	if validationErrors != nil {
		t.Errorf("Errors: %v", kerrors.NewAggregate(validationErrors))
	}
}

func TestValidateBaseRpmImages(t *testing.T) {
	var validationErrors []error
	var testBaseRpmImagesCases = []struct {
		id            string
		baseRpmImages map[string]ImageStreamTagReference
		expectedValid bool
	}{
		{
			id: "base rpm images",
			baseRpmImages: map[string]ImageStreamTagReference{"test": {Cluster: "test"},
				"test2": {Tag: "test2"}, "test3": {Cluster: "test3"},
			},
			expectedValid: false,
		},
	}

	for _, tc := range testBaseRpmImagesCases {
		if errs := validateImageStreamTagReferenceMap("base_rpm_images", tc.baseRpmImages); len(errs) > 0 && tc.expectedValid {
			validationErrors = append(validationErrors, fmt.Errorf("%q expected to be valid, got: %v", tc.id, errs))
		} else if !tc.expectedValid && len(errs) == 0 {
			validationErrors = append(validationErrors, parseValidError(tc.id))
		}
	}
	if validationErrors != nil {
		t.Errorf("Errors: %v", kerrors.NewAggregate(validationErrors))
	}
}

func TestValidateTestSteps(t *testing.T) {
	// string pointers in golang are annoying
	myReference := "my-reference"
	asReference := "as"
	for _, tc := range []struct {
		name  string
		steps []TestStep
		seen  sets.String
		errs  []error
	}{{
		name: "valid step",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:       "as",
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
		}},
	}, {
		name: "no name",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
		}},
		errs: []error{errors.New("test[0]: `as` is required")},
	}, {
		name: "duplicated names",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:       "s0",
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
		}, {
			LiteralTestStep: &LiteralTestStep{
				As:       "s1",
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
		}, {
			LiteralTestStep: &LiteralTestStep{
				As:       "s0",
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
		}},
		errs: []error{errors.New(`test[2]: duplicated name "s0"`)},
	}, {
		name: "duplicated name from other stage",
		seen: sets.NewString("s0"),
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:       "s0",
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
		},
		},
		errs: []error{errors.New(`test[0]: duplicated name "s0"`)},
	}, {
		name: "no image",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:       "no_image",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
		}},
		errs: []error{errors.New("test[0]: `from` is required")},
	}, {
		name: "no commands",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:   "no_commands",
				From: "from",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
		}},
		errs: []error{errors.New("test[0]: `commands` is required")},
	}, {
		name: "invalid resources",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:       "bad_resources",
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "yes"},
					Limits:   ResourceList{"piña_colada": "10dL"},
				}},
		}},
		errs: []error{
			errors.New("'test[0].resources.limits' specifies an invalid key piña_colada"),
			errors.New("test[0].resources.requests.cpu: invalid quantity: quantities must match the regular expression '^([+-]?[0-9.]+)([eEinumkKMGTP]*[-+]?[0-9]*)$'"),
		},
	}, {
		name: "Reference and TestStep set",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:       "as",
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
			Reference: &myReference,
		}},
		errs: []error{
			errors.New("test[0]: only one of `ref`, `chain`, or a literal test step can be set"),
		},
	}, {
		name: "Step with same name as reference",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:       "as",
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				}},
		}, {
			Reference: &asReference,
		}},
		errs: []error{
			errors.New("test[1].ref: duplicated name \"as\""),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			seen := tc.seen
			if seen == nil {
				seen = sets.NewString()
			}
			ret := validateTestSteps("test", tc.steps, seen)
			if !reflect.DeepEqual(ret, tc.errs) {
				t.Fatal(diff.ObjectReflectDiff(ret, tc.errs))
			}
		})
	}
}

func parseValidError(id string) error {
	return fmt.Errorf("%q expected to be invalid, but returned valid", id)
}

func TestValidateResources(t *testing.T) {
	var testCases = []struct {
		name        string
		input       ResourceConfiguration
		expectedErr bool
	}{
		{
			name: "valid configuration makes no error",
			input: ResourceConfiguration{
				"*": ResourceRequirements{
					Limits: ResourceList{
						"cpu": "100m",
					},
					Requests: ResourceList{
						"cpu": "100m",
					},
				},
			},
			expectedErr: false,
		},
		{
			name:        "configuration without any entry fails",
			input:       ResourceConfiguration{},
			expectedErr: true,
		},
		{
			name: "configuration without a blanket entry fails",
			input: ResourceConfiguration{
				"something": ResourceRequirements{
					Limits: ResourceList{
						"cpu": "100m",
					},
					Requests: ResourceList{
						"cpu": "100m",
					},
				},
			},
			expectedErr: true,
		},
		{
			name: "invalid key makes an error",
			input: ResourceConfiguration{
				"*": ResourceRequirements{
					Limits: ResourceList{
						"cpu":    "100m",
						"boogie": "value",
					},
					Requests: ResourceList{
						"cpu": "100m",
					},
				},
			},
			expectedErr: true,
		},
		{
			name: "not having either cpu or memory makes an error",
			input: ResourceConfiguration{
				"*": ResourceRequirements{
					Limits: ResourceList{
						"boogie": "100m",
					},
					Requests: ResourceList{
						"cpu": "100m",
					},
				},
			},
			expectedErr: true,
		},
		{
			name: "invalid value makes an error",
			input: ResourceConfiguration{
				"*": ResourceRequirements{
					Limits: ResourceList{
						"cpu": "donkeys",
					},
					Requests: ResourceList{
						"cpu": "100m",
					},
				},
			},
			expectedErr: true,
		},
		{
			name: "negative value makes an error",
			input: ResourceConfiguration{
				"*": ResourceRequirements{
					Limits: ResourceList{
						"cpu": "-110m",
					},
					Requests: ResourceList{
						"cpu": "100m",
					},
				},
			},
			expectedErr: true,
		},
		{
			name: "zero value makes an error",
			input: ResourceConfiguration{
				"*": ResourceRequirements{
					Limits: ResourceList{
						"cpu": "0m",
					},
					Requests: ResourceList{
						"cpu": "100m",
					},
				},
			},
			expectedErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateResources("", testCase.input)
			if err == nil && testCase.expectedErr {
				t.Errorf("%s: expected an error, but got none", testCase.name)
			}
			if err != nil && !testCase.expectedErr {
				t.Errorf("%s: expected no error, but got one: %v", testCase.name, err)
			}
		})
	}
}
