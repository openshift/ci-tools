package api

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/diff"
)

func TestValidateTests(t *testing.T) {
	for _, tc := range []struct {
		id            string
		release       *ReleaseTagConfiguration
		tests         []TestStepConfiguration
		resolved      bool
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
			id: "invalid secret and secrets both set",
			tests: []TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{},
					Secret: &Secret{
						Name:      "secret_test_a",
						MountPath: "/path/to/secret:exec",
					},
					Secrets: []*Secret{
						{
							Name:      "secret_test_b",
							MountPath: "/path/to/secret:exec",
						},
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid duplicate secret names",
			tests: []TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{},
					Secrets: []*Secret{
						{
							Name:      "secret-test-a",
							MountPath: "/path/to/secret:exec",
						},
						{
							Name:      "secret-test-a",
							MountPath: "/path/to/secret:exec",
						},
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
			id: "valid secrets single entry",
			tests: []TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
					Secrets: []*Secret{
						{
							Name: "secret-a",
						},
					},
				},
			},
			expectedValid: true,
		},
		{
			id: "valid secrets multi entry",
			tests: []TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &ContainerTestConfiguration{From: "ignored"},
					Secrets: []*Secret{
						{
							Name: "secret-a",
						},
						{
							Name: "secret-b",
						},
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
		{
			id:       "non-literal test is invalid in fully-resolved configuration",
			resolved: true,
			tests: []TestStepConfiguration{
				{
					As:                          "non-literal",
					MultiStageTestConfiguration: &MultiStageTestConfiguration{},
				},
			},
		},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if errs := validateTestStepConfiguration("tests", tc.tests, tc.release, tc.resolved); len(errs) > 0 && tc.expectedValid {
				t.Errorf("expected to be valid, got: %v", errs)
			} else if !tc.expectedValid && len(errs) == 0 {
				t.Error("expected to be invalid, but returned valid")
			}
		})
	}
}

func TestValidateBuildRoot(t *testing.T) {
	for _, tc := range []struct {
		id                   string
		buildRootImageConfig *BuildRootImageConfiguration
		hasImages            bool
		expectedValid        bool
	}{
		{
			id: "both project_image and image_stream_tag in build_root defined causes error",
			buildRootImageConfig: &BuildRootImageConfiguration{
				ImageStreamTagReference: &ImageStreamTagReference{
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
	} {
		t.Run(tc.id, func(t *testing.T) {
			if errs := validateBuildRootImageConfiguration("build_root", tc.buildRootImageConfig, tc.hasImages); len(errs) > 0 && tc.expectedValid {
				t.Errorf("expected to be valid, got: %v", errs)
			} else if !tc.expectedValid && len(errs) == 0 {
				t.Error("expected to be invalid, but returned valid")
			}
		})
	}
}

func TestValidateBaseImages(t *testing.T) {
	for _, tc := range []struct {
		id            string
		baseImages    map[string]ImageStreamTagReference
		expectedValid bool
	}{
		{
			id: "base images",
			baseImages: map[string]ImageStreamTagReference{"test": {},
				"test2": {Tag: "test2"}, "test3": {},
			},
			expectedValid: false,
		},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if errs := validateImageStreamTagReferenceMap("base_images", tc.baseImages); len(errs) > 0 && tc.expectedValid {
				t.Errorf("expected to be valid, got: %v", errs)
			} else if !tc.expectedValid && len(errs) == 0 {
				t.Error("expected to be invalid, but returned valid")
			}
		})
	}
}

func TestValidateBaseRpmImages(t *testing.T) {
	for _, tc := range []struct {
		id            string
		baseRpmImages map[string]ImageStreamTagReference
		expectedValid bool
	}{
		{
			id: "base rpm images",
			baseRpmImages: map[string]ImageStreamTagReference{"test": {},
				"test2": {Tag: "test2"}, "test3": {},
			},
			expectedValid: false,
		},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if errs := validateImageStreamTagReferenceMap("base_rpm_images", tc.baseRpmImages); len(errs) > 0 && tc.expectedValid {
				t.Errorf("expected to be valid, got: %v", errs)
			} else if !tc.expectedValid && len(errs) == 0 {
				t.Error("expected to be invalid, but returned valid")
			}
		})
	}
}

func TestValidateTestSteps(t *testing.T) {
	resources := ResourceRequirements{
		Requests: ResourceList{"cpu": "1"},
		Limits:   ResourceList{"memory": "1m"},
	}
	// string pointers in golang are annoying
	myReference := "my-reference"
	asReference := "as"
	yes := true
	for _, tc := range []struct {
		name  string
		steps []TestStep
		seen  sets.String
		errs  []error
	}{{
		name: "valid step",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:        "as",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}},
	}, {
		name: "no name",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0]: `as` is required")},
	}, {
		name: "duplicated names",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:        "s0",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}, {
			LiteralTestStep: &LiteralTestStep{
				As:        "s1",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}, {
			LiteralTestStep: &LiteralTestStep{
				As:        "s0",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New(`test[2]: duplicated name "s0"`)},
	}, {
		name: "duplicated name from other stage",
		seen: sets.NewString("s0"),
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:        "s0",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		},
		},
		errs: []error{errors.New(`test[0]: duplicated name "s0"`)},
	}, {
		name: "no image",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:        "no_image",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0]: `from` or `from_image` is required")},
	}, {
		name: "two images",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:   "no_image",
				From: "something",
				FromImage: &ImageStreamTagReference{
					Namespace: "ns",
					Name:      "name",
					Tag:       "tag",
				},
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0]: `from` and `from_image` cannot be set together")},
	}, {
		name: "from_image missing namespace",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As: "no_image",
				FromImage: &ImageStreamTagReference{
					Name: "name",
					Tag:  "tag",
				},
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from_image: `namespace` is required")},
	}, {
		name: "from_image missing name",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As: "no_image",
				FromImage: &ImageStreamTagReference{
					Namespace: "ns",
					Tag:       "tag",
				},
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from_image: `name` is required")},
	}, {
		name: "from_image missing tag",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As: "no_image",
				FromImage: &ImageStreamTagReference{
					Namespace: "ns",
					Name:      "name",
				},
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from_image: `tag` is required")},
	}, {
		name: "invalid image 0",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:        "as",
				From:      "docker.io/library/centos:7",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from: 'docker.io/library/centos:7' is not a valid Kubernetes object name")},
	}, {
		name: "invalid image 1",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:        "as",
				From:      "stable:base",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from: 'stable:base' is not a valid Kubernetes object name")},
	}, {
		name: "no commands",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:        "no_commands",
				From:      "from",
				Resources: resources},
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
				As:        "as",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
			Reference: &myReference,
		}},
		errs: []error{
			errors.New("test[0]: only one of `ref`, `chain`, or a literal test step can be set"),
		},
	}, {
		name: "Step with same name as reference",
		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:        "as",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}, {
			Reference: &asReference,
		}},
		errs: []error{
			errors.New("test[1].ref: duplicated name \"as\""),
		},
	}, {
		name: "Test step with forbidden parameter",

		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:                "as",
				From:              "from",
				Commands:          "commands",
				Resources:         resources,
				OptionalOnSuccess: &yes},
		}},
		errs: []error{
			errors.New("test[0]: `optional_on_success` is only allowed for Post steps"),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			seen := tc.seen
			if seen == nil {
				seen = sets.NewString()
			}
			ret := validateTestStepsTest("test", tc.steps, seen, nil)
			if !errListMessagesEqual(ret, tc.errs) {
				t.Fatal(diff.ObjectReflectDiff(ret, tc.errs))
			}
		})
	}
}

func TestValidatePostSteps(t *testing.T) {
	resources := ResourceRequirements{
		Requests: ResourceList{"cpu": "1"},
		Limits:   ResourceList{"memory": "1m"},
	}
	yes := true
	for _, tc := range []struct {
		name  string
		steps []TestStep
		seen  sets.String
		errs  []error
	}{{
		name: "Valid Post steps",

		steps: []TestStep{{
			LiteralTestStep: &LiteralTestStep{
				As:                "as",
				From:              "from",
				Commands:          "commands",
				Resources:         resources,
				OptionalOnSuccess: &yes},
		}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			seen := tc.seen
			if seen == nil {
				seen = sets.NewString()
			}
			ret := validateTestStepsPost("test", tc.steps, seen, nil)
			if !errListMessagesEqual(ret, tc.errs) {
				t.Fatal(diff.ObjectReflectDiff(ret, tc.errs))
			}
		})
	}
}

func TestValidateParameters(t *testing.T) {
	defaultStr := "default"
	for _, tc := range []struct {
		name   string
		params []StepParameter
		env    TestEnvironment
		err    []error
	}{{
		name: "no parameters",
	}, {
		name:   "has parameter, parameter provided",
		params: []StepParameter{{Name: "TEST"}},
		env:    TestEnvironment{"TEST": "test"},
	}, {
		name:   "has parameter with default, no parameter provided",
		params: []StepParameter{{Name: "TEST", Default: &defaultStr}},
	}, {
		name:   "has parameters, some not provided",
		params: []StepParameter{{Name: "TEST0"}, {Name: "TEST1"}},
		env:    TestEnvironment{"TEST0": "test0"},
		err:    []error{errors.New("test: unresolved parameter(s): [TEST1]")},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLiteralTestStepTest("test", LiteralTestStep{
				As:       "as",
				From:     "from",
				Commands: "commands",
				Resources: ResourceRequirements{
					Requests: ResourceList{"cpu": "1"},
					Limits:   ResourceList{"memory": "1m"},
				},
				Environment: tc.params,
			}, sets.NewString(), tc.env)
			if diff := diff.ObjectReflectDiff(err, tc.err); diff != "<no diffs>" {
				t.Errorf("incorrect error: %s", diff)
			}
		})
	}
}

func TestValidateResources(t *testing.T) {
	for _, testCase := range []struct {
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
	} {
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

func TestValidatePromotion(t *testing.T) {
	var testCases = []struct {
		name     string
		input    PromotionConfiguration
		expected []error
	}{
		{
			name:     "normal config by name is valid",
			input:    PromotionConfiguration{Namespace: "foo", Name: "bar"},
			expected: nil,
		},
		{
			name:     "normal config by tag is valid",
			input:    PromotionConfiguration{Namespace: "foo", Tag: "bar"},
			expected: nil,
		},
		{
			name:     "config missing fields yields errors",
			input:    PromotionConfiguration{},
			expected: []error{errors.New("promotion: no namespace defined"), errors.New("promotion: no name or tag defined")},
		},
		{
			name:     "config with extra fields yields errors",
			input:    PromotionConfiguration{Namespace: "foo", Name: "bar", Tag: "baz"},
			expected: []error{errors.New("promotion: both name and tag defined")},
		},
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			if actual, expected := validatePromotionConfiguration("promotion", test.input), test.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %v", test.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}

func TestValidateReleaseTagConfiguration(t *testing.T) {
	var testCases = []struct {
		name     string
		input    ReleaseTagConfiguration
		expected []error
	}{
		{
			name:     "valid tag_specification",
			input:    ReleaseTagConfiguration{Name: "test", Namespace: "test"},
			expected: nil,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateReleaseTagConfiguration("tag_specification", testCase.input), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %v", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}

func TestValidateCredentials(t *testing.T) {
	var testCases = []struct {
		name   string
		input  []CredentialReference
		output []error
	}{
		{
			name: "no creds means no error",
		},
		{
			name: "cred mount with no name means error",
			input: []CredentialReference{
				{Namespace: "ns", MountPath: "/foo"},
			},
			output: []error{
				errors.New("root.credentials[0].name cannot be empty"),
			},
		},
		{
			name: "cred mount with no namespace means error",
			input: []CredentialReference{
				{Name: "name", MountPath: "/foo"},
			},
			output: []error{
				errors.New("root.credentials[0].namespace cannot be empty"),
			},
		},
		{
			name: "cred mount with no path means error",
			input: []CredentialReference{
				{Namespace: "ns", Name: "name"},
			},
			output: []error{
				errors.New("root.credentials[0].mountPath cannot be empty"),
			},
		},
		{
			name: "cred mount with relative path means error",
			input: []CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "./foo"},
			},
			output: []error{
				errors.New("root.credentials[0].mountPath is not absolute: ./foo"),
			},
		},
		{
			name: "normal creds means no error",
			input: []CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/foo"},
				{Namespace: "ns", Name: "name", MountPath: "/bar"},
			},
		},
		{
			name: "duped cred mount path means error",
			input: []CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/foo"},
				{Namespace: "ns", Name: "name", MountPath: "/foo"},
			},
			output: []error{
				errors.New("root.credentials[0] and credentials[1] mount to the same location (/foo)"),
			},
		},
		{
			name: "subdir cred mount path means error",
			input: []CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/foo/bar"},
				{Namespace: "ns", Name: "name", MountPath: "/foo"},
				{Namespace: "ns", Name: "name", MountPath: "/foo/bar/baz"},
			},
			output: []error{
				errors.New("root.credentials[0] mounts at /foo/bar, which is under credentials[1] (/foo)"),
				errors.New("root.credentials[2] mounts at /foo/bar/baz, which is under credentials[0] (/foo/bar)"),
				errors.New("root.credentials[2] mounts at /foo/bar/baz, which is under credentials[1] (/foo)"),
			},
		},
		{
			name: "substring cred mount path means no error",
			input: []CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/foo-bar"},
				{Namespace: "ns", Name: "name", MountPath: "/foo"},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateCredentials("root", testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidateReleases(t *testing.T) {
	var testCases = []struct {
		name       string
		input      map[string]UnresolvedRelease
		hasTagSpec bool
		output     []error
	}{
		{
			name:  "no releases",
			input: map[string]UnresolvedRelease{},
		},
		{
			name: "valid releases",
			input: map[string]UnresolvedRelease{
				"first": {
					Candidate: &Candidate{
						Product:      ReleaseProductOKD,
						Architecture: ReleaseArchitectureAMD64,
						Stream:       ReleaseStreamOKD,
						Version:      "4.4",
					},
				},
				"second": {
					Release: &Release{
						Architecture: ReleaseArchitectureAMD64,
						Channel:      ReleaseChannelCandidate,
						Version:      "4.4",
					},
				},
				"third": {
					Prerelease: &Prerelease{
						Product:      ReleaseProductOCP,
						Architecture: ReleaseArchitectureS390x,
						VersionBounds: VersionBounds{
							Lower: "4.1.0",
							Upper: "4.2.0",
						},
					},
				},
			},
		},
		{
			name: "invalid use of latest release with tag spec",
			input: map[string]UnresolvedRelease{
				"latest": {
					Candidate: &Candidate{
						Product:      ReleaseProductOKD,
						Architecture: ReleaseArchitectureAMD64,
						Stream:       ReleaseStreamOKD,
						Version:      "4.4",
					},
				},
			},
			hasTagSpec: true,
			output: []error{
				errors.New("root.latest: cannot request resolving a latest release and set tag_specification"),
			},
		},
		{
			name: "invalid release with no options set",
			input: map[string]UnresolvedRelease{
				"latest": {},
			},
			output: []error{
				errors.New("root.latest: must set candidate, prerelease or release"),
			},
		},
		{
			name: "invalid release with two options set",
			input: map[string]UnresolvedRelease{
				"latest": {
					Candidate: &Candidate{},
					Release:   &Release{},
				},
			},
			output: []error{
				errors.New("root.latest: cannot set more than one of candidate, prerelease and release"),
			},
		},
		{
			name: "invalid release with all options set",
			input: map[string]UnresolvedRelease{
				"latest": {
					Candidate:  &Candidate{},
					Release:    &Release{},
					Prerelease: &Prerelease{},
				},
			},
			output: []error{
				errors.New("root.latest: cannot set more than one of candidate, prerelease and release"),
			},
		},
		{
			name: "invalid releases",
			input: map[string]UnresolvedRelease{
				"first": {
					Candidate: &Candidate{
						Product:      "bad",
						Architecture: ReleaseArchitectureAMD64,
						Stream:       ReleaseStreamOKD,
						Version:      "4.4",
					},
				},
				"second": {
					Release: &Release{
						Architecture: ReleaseArchitectureAMD64,
						Channel:      "ew",
						Version:      "4.4",
					},
				},
				"third": {
					Prerelease: &Prerelease{
						Product:      ReleaseProductOCP,
						Architecture: ReleaseArchitectureS390x,
						VersionBounds: VersionBounds{
							Lower: "4.1.0",
						},
					},
				},
			},
			hasTagSpec: true,
			output: []error{
				errors.New("root.first.product: must be one of ocp, okd"),
				errors.New("root.second.channel: must be one of candidate, fast, stable"),
				errors.New("root.third.version_bounds.upper: must be set"),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateReleases("root", testCase.input, testCase.hasTagSpec), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidateCandidate(t *testing.T) {
	var testCases = []struct {
		name   string
		input  Candidate
		output []error
	}{
		{
			name: "valid candidate",
			input: Candidate{
				Product:      ReleaseProductOKD,
				Architecture: ReleaseArchitectureAMD64,
				Stream:       ReleaseStreamOKD,
				Version:      "4.4",
				Relative:     10,
			},
		},
		{
			name: "valid candidate for ocp",
			input: Candidate{
				Product:      ReleaseProductOCP,
				Architecture: ReleaseArchitectureS390x,
				Stream:       ReleaseStreamNightly,
				Version:      "4.5",
			},
		},
		{
			name: "valid candidate with defaulted arch",
			input: Candidate{
				Product: ReleaseProductOKD,
				Stream:  ReleaseStreamOKD,
				Version: "4.4",
			},
		},
		{
			name: "valid candidate with defaulted arch and okd stream",
			input: Candidate{
				Product: ReleaseProductOKD,
				Version: "4.4",
			},
		},
		{
			name: "invalid candidate from arch",
			input: Candidate{
				Product:      ReleaseProductOKD,
				Architecture: "oops",
				Stream:       ReleaseStreamOKD,
				Version:      "4.4",
			},
			output: []error{
				errors.New("root.architecture: must be one of amd64, ppc64le, s390x"),
			},
		},
		{
			name: "invalid candidate from product",
			input: Candidate{
				Product:      "whoa",
				Architecture: ReleaseArchitectureAMD64,
				Stream:       ReleaseStreamOKD,
				Version:      "4.4",
			},
			output: []error{
				errors.New("root.product: must be one of ocp, okd"),
			},
		},
		{
			name: "invalid candidate from stream",
			input: Candidate{
				Product:      ReleaseProductOKD,
				Architecture: ReleaseArchitectureAMD64,
				Stream:       ReleaseStreamCI,
				Version:      "4.4",
			},
			output: []error{
				errors.New("root.stream: must be one of , okd"),
			},
		},
		{
			name: "invalid candidate from version",
			input: Candidate{
				Product:      ReleaseProductOKD,
				Architecture: ReleaseArchitectureAMD64,
				Stream:       ReleaseStreamOKD,
				Version:      "4",
			},
			output: []error{
				errors.New(`root.version: must be a minor version in the form [0-9]\.[0-9]+`),
			},
		},
		{
			name: "invalid candidate from ocp stream",
			input: Candidate{
				Product:      ReleaseProductOCP,
				Architecture: ReleaseArchitectureAMD64,
				Stream:       ReleaseStreamOKD,
				Version:      "4.4",
			},
			output: []error{
				errors.New("root.stream: must be one of ci, nightly"),
			},
		},
		{
			name: "invalid relative",
			input: Candidate{
				Product:      ReleaseProductOCP,
				Architecture: ReleaseArchitectureAMD64,
				Stream:       ReleaseStreamCI,
				Version:      "4.4",
				Relative:     -1,
			},
			output: []error{
				errors.New("root.relative: must be a positive integer"),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateCandidate("root", testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidateImages(t *testing.T) {
	var testCases = []struct {
		name   string
		input  []ProjectDirectoryImageBuildStepConfiguration
		output []error
	}{
		{
			name: "no images",
		},
		{
			name: "valid images",
			input: []ProjectDirectoryImageBuildStepConfiguration{
				{ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				{ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile.rhel7"}},
			},
		},
		{
			name: "reuse of literal Dockerfile",
			input: []ProjectDirectoryImageBuildStepConfiguration{
				{ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				{ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
			},
			output: []error{errors.New("root[1]: this image build uses a Dockerfile at Dockerfile, which is already used to build another image - not two images may be built from one Dockerfile")},
		},
		{
			name: "reuse of Dockerfile via context dir",
			input: []ProjectDirectoryImageBuildStepConfiguration{
				{ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "path/Dockerfile"}},
				{ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile", ContextDir: "path"}},
			},
			output: []error{errors.New("root[1]: this image build uses a Dockerfile at path/Dockerfile, which is already used to build another image - not two images may be built from one Dockerfile")},
		},
		{
			name: "reuse of Dockerfile via context dir without explicit paths",
			input: []ProjectDirectoryImageBuildStepConfiguration{
				{ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{ContextDir: "path"}},
				{ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{ContextDir: "path"}},
			},
			output: []error{errors.New("root[1]: this image build uses a Dockerfile at path/Dockerfile, which is already used to build another image - not two images may be built from one Dockerfile")},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateImages("root", testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidateRelease(t *testing.T) {
	var testCases = []struct {
		name   string
		input  Release
		output []error
	}{
		{
			name: "valid release",
			input: Release{
				Architecture: ReleaseArchitectureAMD64,
				Channel:      ReleaseChannelCandidate,
				Version:      "4.4",
			},
		},
		{
			name: "valid release with defaulted arch",
			input: Release{
				Version: "4.4",
				Channel: ReleaseChannelCandidate,
			},
		},
		{
			name: "invalid release from arch",
			input: Release{
				Architecture: "oops",
				Channel:      ReleaseChannelFast,
				Version:      "4.4",
			},
			output: []error{
				errors.New("root.architecture: must be one of amd64, ppc64le, s390x"),
			},
		},
		{
			name: "invalid release from channel",
			input: Release{
				Architecture: ReleaseArchitectureAMD64,
				Channel:      "oops",
				Version:      "4.4",
			},
			output: []error{
				errors.New("root.channel: must be one of candidate, fast, stable"),
			},
		},

		{
			name: "invalid release from version",
			input: Release{
				Architecture: ReleaseArchitectureAMD64,
				Channel:      ReleaseChannelStable,
				Version:      "4",
			},
			output: []error{
				errors.New(`root.version: must be a minor version in the form [0-9]\.[0-9]+`),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateRelease("root", testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidatePrerelease(t *testing.T) {
	var testCases = []struct {
		name   string
		input  Prerelease
		output []error
	}{
		{
			name: "valid prerelease",
			input: Prerelease{
				Product:      ReleaseProductOKD,
				Architecture: ReleaseArchitectureAMD64,
				VersionBounds: VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
		},
		{
			name: "valid prerelease for ocp",
			input: Prerelease{
				Product:      ReleaseProductOCP,
				Architecture: ReleaseArchitectureS390x,
				VersionBounds: VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
		},
		{
			name: "valid prerelease with defaulted arch",
			input: Prerelease{
				Product: ReleaseProductOKD,
				VersionBounds: VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
		},
		{
			name: "invalid prerelease from arch",
			input: Prerelease{
				Product:      ReleaseProductOKD,
				Architecture: "oops",
				VersionBounds: VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
			output: []error{
				errors.New("root.architecture: must be one of amd64, ppc64le, s390x"),
			},
		},
		{
			name: "invalid prerelease from product",
			input: Prerelease{
				Product:      "whoa",
				Architecture: ReleaseArchitectureAMD64,
				VersionBounds: VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
			output: []error{
				errors.New("root.product: must be one of ocp, okd"),
			},
		},
		{
			name: "invalid prerelease from missing version bounds",
			input: Prerelease{
				Product:       ReleaseProductOCP,
				Architecture:  ReleaseArchitectureAMD64,
				VersionBounds: VersionBounds{},
			},
			output: []error{
				errors.New("root.version_bounds.lower: must be set"),
				errors.New("root.version_bounds.upper: must be set"),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validatePrerelease("root", testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func errListMessagesEqual(a, b []error) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if (a[idx] == nil) != (b[idx] == nil) {
			return false
		}
		if a[idx].Error() != b[idx].Error() {
			return false
		}
	}
	return true
}
