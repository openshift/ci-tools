package validation

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/diff"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestValidateTests(t *testing.T) {
	cronString := "0 0 * * 1"
	invalidCronString := "r 0 * * 1"
	intervalString := "6h"
	invalidIntervalString := "6t"
	for _, tc := range []struct {
		id            string
		release       *api.ReleaseTagConfiguration
		releases      sets.String
		tests         []api.TestStepConfiguration
		resolved      bool
		expectedValid bool
	}{
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "unit"}}`,
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: true,
		},
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "images"}}`,
			tests: []api.TestStepConfiguration{
				{
					As:                         "images",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "ci-index"}}`,
			tests: []api.TestStepConfiguration{
				{
					As:                         "ci-index",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "ci-index-my-bundle"}}`,
			tests: []api.TestStepConfiguration{
				{
					As:                         "ci-index-my-bundle",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: "No test type",
			tests: []api.TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
				},
			},
			expectedValid: false,
		},
		{
			id: "Multiple test types",
			tests: []api.TestStepConfiguration{
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{},
					OpenshiftAnsibleClusterTestConfiguration: &api.OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{
							ClusterProfile: api.ClusterProfileAWSAtomic,
						},
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "`commands` and `steps`",
			tests: []api.TestStepConfiguration{
				{
					As:                          "test",
					Commands:                    "commands",
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{},
				},
			},
			expectedValid: false,
		},
		{
			id: "container test without `from`",
			tests: []api.TestStepConfiguration{
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{},
				},
			},
			expectedValid: false,
		},
		{
			id: "test without `commands`",
			tests: []api.TestStepConfiguration{
				{
					As:                         "test",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: "test valid memory backed volume",
			tests: []api.TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{
						From: "ignored",
						MemoryBackedVolume: &api.MemoryBackedVolume{
							Size: "1Gi",
						},
					},
				},
			},
			expectedValid: true,
		},
		{
			id: "test invalid memory backed volume",
			tests: []api.TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{
						From: "ignored",
						MemoryBackedVolume: &api.MemoryBackedVolume{
							Size: "1GG", // not valid
						},
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "test with duplicated `as`",
			tests: []api.TestStepConfiguration{
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: "test without `as`",
			tests: []api.TestStepConfiguration{
				{
					Commands:                   "test",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid cluster profile",
			tests: []api.TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &api.OpenshiftAnsibleClusterTestConfiguration{},
				},
			},
			expectedValid: false,
		},
		{
			id: "release missing",
			tests: []api.TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					OpenshiftAnsibleClusterTestConfiguration: &api.OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileGCP},
					},
				},
			},
		},
		{
			id: "release must be origin",
			tests: []api.TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					OpenshiftAnsibleClusterTestConfiguration: &api.OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileGCP},
					},
				},
			},
			release:       &api.ReleaseTagConfiguration{},
			expectedValid: true,
		},
		{
			id: "with release",
			tests: []api.TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					OpenshiftAnsibleClusterTestConfiguration: &api.OpenshiftAnsibleClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileGCP},
					},
				},
			},
			release:       &api.ReleaseTagConfiguration{Name: "origin-v3.11"},
			expectedValid: true,
		},
		{
			id: "invalid secret mountPath",
			tests: []api.TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &api.OpenshiftAnsibleClusterTestConfiguration{},
					Secret: &api.Secret{
						Name:      "secret",
						MountPath: "/path/to/secret:exec",
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid secret name",
			tests: []api.TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &api.OpenshiftAnsibleClusterTestConfiguration{},
					Secret: &api.Secret{
						Name:      "secret_test",
						MountPath: "/path/to/secret:exec",
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid secret and secrets both set",
			tests: []api.TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &api.OpenshiftAnsibleClusterTestConfiguration{},
					Secret: &api.Secret{
						Name:      "secret_test_a",
						MountPath: "/path/to/secret:exec",
					},
					Secrets: []*api.Secret{
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
			tests: []api.TestStepConfiguration{
				{
					As:                                       "test",
					OpenshiftAnsibleClusterTestConfiguration: &api.OpenshiftAnsibleClusterTestConfiguration{},
					Secrets: []*api.Secret{
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
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Secret: &api.Secret{
						Name: "secret",
					},
				},
			},
			expectedValid: true,
		},
		{
			id: "valid secrets single entry",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Secrets: []*api.Secret{
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
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Secrets: []*api.Secret{
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
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Secret: &api.Secret{
						Name:      "secret",
						MountPath: "/path/to/secret",
					},
				},
			},
			expectedValid: true,
		},
		{
			id: "valid secret with invalid path",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Secret: &api.Secret{
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
			tests: []api.TestStepConfiguration{
				{
					As:                          "non-literal",
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{},
				},
			},
		},
		{
			id: "cron and postsubmit together are invalid",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Cron:                       &cronString,
					Postsubmit:                 true,
				},
			},
			expectedValid: false,
		},
		{
			id: "valid cron",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Cron:                       &cronString,
				},
			},
			expectedValid: true,
		},
		{
			id: "valid interval",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Interval:                   &intervalString,
				},
			},
			expectedValid: true,
		},
		{
			id: "cron and interval together are invalid",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Cron:                       &cronString,
					Interval:                   &intervalString,
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid cron",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Cron:                       &invalidCronString,
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid interval",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Interval:                   &invalidIntervalString,
				},
			},
			expectedValid: false,
		},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if errs := validateTestStepConfiguration("tests", tc.tests, tc.release, tc.releases, tc.resolved); len(errs) > 0 && tc.expectedValid {
				t.Errorf("expected to be valid, got: %v", errs)
			} else if !tc.expectedValid && len(errs) == 0 {
				t.Error("expected to be invalid, but returned valid")
			}
		})
	}
}

func TestValidateTestSteps(t *testing.T) {
	resources := api.ResourceRequirements{
		Requests: api.ResourceList{"cpu": "1"},
		Limits:   api.ResourceList{"memory": "1m"},
	}
	// string pointers in golang are annoying
	myReference := "my-reference"
	asReference := "as"
	yes := true
	for _, tc := range []struct {
		name     string
		steps    []api.TestStep
		seen     sets.String
		errs     []error
		releases sets.String
	}{{
		name: "valid step",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "as",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}},
	}, {
		name: "valid kvm",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:       "as",
				From:     "from",
				Commands: "commands",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"devices.kubevirt.io/kvm": "1"},
					Limits:   api.ResourceList{"devices.kubevirt.io/kvm": "1"},
				},
			},
		}},
	}, {
		name: "no name",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0]: `as` is required")},
	}, {
		name: "duplicated names",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "s0",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}, {
			LiteralTestStep: &api.LiteralTestStep{
				As:        "s1",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}, {
			LiteralTestStep: &api.LiteralTestStep{
				As:        "s0",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New(`test[2]: duplicated name "s0"`)},
	}, {
		name: "duplicated name from other stage",
		seen: sets.NewString("s0"),
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "s0",
				From:      "from",
				Commands:  "commands",
				Resources: resources},
		},
		},
		errs: []error{errors.New(`test[0]: duplicated name "s0"`)},
	}, {
		name: "no image",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "no_image",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0]: `from` or `from_image` is required")},
	}, {
		name: "two images",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:   "no_image",
				From: "something",
				FromImage: &api.ImageStreamTagReference{
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
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As: "no_image",
				FromImage: &api.ImageStreamTagReference{
					Name: "name",
					Tag:  "tag",
				},
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from_image: `namespace` is required")},
	}, {
		name: "from_image missing name",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As: "no_image",
				FromImage: &api.ImageStreamTagReference{
					Namespace: "ns",
					Tag:       "tag",
				},
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from_image: `name` is required")},
	}, {
		name: "from_image missing tag",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As: "no_image",
				FromImage: &api.ImageStreamTagReference{
					Namespace: "ns",
					Name:      "name",
				},
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from_image: `tag` is required")},
	}, {
		name: "invalid image 0",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "as",
				From:      "docker.io/library/centos:7",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from: 'docker.io/library/centos' is not a valid Kubernetes object name")},
	}, {
		name: "invalid image 1",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "as",
				From:      "stable>initial:base",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from: 'stable>initial' is not a valid Kubernetes object name")},
	}, {
		name: "invalid image 2",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "as",
				From:      "stable:initial:base",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from: 'stable:initial:base' is not a valid imagestream reference")},
	}, {
		name: "invalid image 3",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "as",
				From:      "no-such-imagestream:base",
				Commands:  "commands",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0].from: unknown imagestream 'no-such-imagestream'")},
	}, {
		name: "custom imagestream",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "as",
				From:      "stable-previous:base",
				Commands:  "commands",
				Resources: resources},
		}},
		releases: sets.NewString("previous"),
	}, {
		name: "invalid image 4",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "as",
				From:      "stable-nonexistent:base",
				Commands:  "commands",
				Resources: resources},
		}},
		releases: sets.NewString("previous"),
		errs:     []error{errors.New("test[0].from: unknown imagestream 'stable-nonexistent'")},
	}, {
		name: "no commands",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "no_commands",
				From:      "from",
				Resources: resources},
		}},
		errs: []error{errors.New("test[0]: `commands` is required")},
	}, {
		name: "invalid resources",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:       "bad_resources",
				From:     "from",
				Commands: "commands",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "yes"},
					Limits:   api.ResourceList{"piña_colada": "10dL"},
				}},
		}},
		errs: []error{
			errors.New("'test[0].resources.limits' specifies an invalid key piña_colada"),
			errors.New("test[0].resources.requests.cpu: invalid quantity: quantities must match the regular expression '^([+-]?[0-9.]+)([eEinumkKMGTP]*[-+]?[0-9]*)$'"),
		},
	}, {
		name: "Reference and TestStep set",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
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
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
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

		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:                "as",
				From:              "from",
				Commands:          "commands",
				Resources:         resources,
				OptionalOnSuccess: &yes},
		}},
		errs: []error{
			errors.New("test[0]: `optional_on_success` is only allowed for Post steps"),
		},
	}, {
		name: "Multiple errors",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				From:      "from",
				Commands:  "commands",
				Resources: resources,
			},
		}, {
			LiteralTestStep: &api.LiteralTestStep{
				From:      "from",
				Commands:  "commands",
				Resources: resources,
			},
		}},
		errs: []error{
			errors.New("test[0]: `as` is required"),
			errors.New("test[1]: `as` is required"),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			context := newContext("test", nil, tc.releases)
			if tc.seen != nil {
				context.seen = tc.seen
			}
			ret := validateTestSteps(context, testStageTest, tc.steps)
			if len(ret) > 0 && len(tc.errs) == 0 {
				t.Fatalf("Unexpected error %v", ret)
			}
			if !errListMessagesEqual(ret, tc.errs) {
				t.Fatal(diff.ObjectReflectDiff(ret, tc.errs))
			}
		})
	}
}

func TestValidatePostSteps(t *testing.T) {
	resources := api.ResourceRequirements{
		Requests: api.ResourceList{"cpu": "1"},
		Limits:   api.ResourceList{"memory": "1m"},
	}
	yes := true
	for _, tc := range []struct {
		name     string
		steps    []api.TestStep
		seen     sets.String
		errs     []error
		releases sets.String
	}{{
		name: "Valid Post steps",

		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:                "as",
				From:              "from",
				Commands:          "commands",
				Resources:         resources,
				OptionalOnSuccess: &yes},
		}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			context := newContext("test", nil, tc.releases)
			if tc.seen != nil {
				context.seen = tc.seen
			}
			ret := validateTestSteps(context, testStagePost, tc.steps)
			if !errListMessagesEqual(ret, tc.errs) {
				t.Fatal(diff.ObjectReflectDiff(ret, tc.errs))
			}
		})
	}
}

func TestValidateParameters(t *testing.T) {
	defaultStr := "default"
	for _, tc := range []struct {
		name     string
		params   []api.StepParameter
		env      api.TestEnvironment
		err      []error
		releases sets.String
	}{{
		name: "no parameters",
	}, {
		name:   "has parameter, parameter provided",
		params: []api.StepParameter{{Name: "TEST"}},
		env:    api.TestEnvironment{"TEST": "test"},
	}, {
		name:   "has parameter with default, no parameter provided",
		params: []api.StepParameter{{Name: "TEST", Default: &defaultStr}},
	}, {
		name:   "has parameters, some not provided",
		params: []api.StepParameter{{Name: "TEST0"}, {Name: "TEST1"}},
		env:    api.TestEnvironment{"TEST0": "test0"},
		err:    []error{errors.New("test: unresolved parameter(s): [TEST1]")},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLiteralTestStep(newContext("test", tc.env, tc.releases), testStageTest, api.LiteralTestStep{
				As:       "as",
				From:     "from",
				Commands: "commands",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1"},
					Limits:   api.ResourceList{"memory": "1m"},
				},
				Environment: tc.params,
			})
			if diff := diff.ObjectReflectDiff(err, tc.err); diff != "<no diffs>" {
				t.Errorf("incorrect error: %s", diff)
			}
		})
	}
}

func TestValidateCredentials(t *testing.T) {
	var testCases = []struct {
		name   string
		input  []api.CredentialReference
		output []error
	}{
		{
			name: "no creds means no error",
		},
		{
			name: "cred mount with no name means error",
			input: []api.CredentialReference{
				{Namespace: "ns", MountPath: "/foo"},
			},
			output: []error{
				errors.New("root.credentials[0].name cannot be empty"),
			},
		},
		{
			name: "cred mount with no namespace means error",
			input: []api.CredentialReference{
				{Name: "name", MountPath: "/foo"},
			},
			output: []error{
				errors.New("root.credentials[0].namespace cannot be empty"),
			},
		},
		{
			name: "cred mount with no path means error",
			input: []api.CredentialReference{
				{Namespace: "ns", Name: "name"},
			},
			output: []error{
				errors.New("root.credentials[0].mountPath cannot be empty"),
			},
		},
		{
			name: "cred mount with relative path means error",
			input: []api.CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "./foo"},
			},
			output: []error{
				errors.New("root.credentials[0].mountPath is not absolute: ./foo"),
			},
		},
		{
			name: "normal creds means no error",
			input: []api.CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/foo"},
				{Namespace: "ns", Name: "name", MountPath: "/bar"},
			},
		},
		{
			name: "duped cred mount path means error",
			input: []api.CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/foo"},
				{Namespace: "ns", Name: "name", MountPath: "/foo"},
			},
			output: []error{
				errors.New("root.credentials[0] and credentials[1] mount to the same location (/foo)"),
			},
		},
		{
			name: "subdir cred mount path means error",
			input: []api.CredentialReference{
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
			input: []api.CredentialReference{
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

func TestValidateDependencies(t *testing.T) {
	var testCases = []struct {
		name   string
		input  []api.StepDependency
		output []error
	}{
		{
			name:  "no dependencies",
			input: nil,
		},
		{
			name: "valid dependencies",
			input: []api.StepDependency{
				{Name: "src", Env: "SOURCE"},
				{Name: "stable:installer", Env: "INSTALLER"},
			},
		},
		{
			name: "invalid dependencies",
			input: []api.StepDependency{
				{Name: "", Env: ""},
				{Name: "src", Env: "SOURCE"},
				{Name: "src", Env: "SOURCE"},
				{Name: "src:lol:oops", Env: "WHOA"},
			},
			output: []error{
				errors.New("root.dependencies[0].name must be set"),
				errors.New("root.dependencies[0].env must be set"),
				errors.New("root.dependencies[2].env targets an environment variable that is already set by another dependency"),
				errors.New("root.dependencies[3].name must take the `tag` or `stream:tag` form, not \"src:lol:oops\""),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateDependencies("root", testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidateLeases(t *testing.T) {
	for _, tc := range []struct {
		name string
		test api.MultiStageTestConfigurationLiteral
		err  []error
	}{{
		name: "valid leases",
		test: api.MultiStageTestConfigurationLiteral{
			Leases: []api.StepLease{
				{ResourceType: "aws-quota-slice", Env: "AWS_LEASED_RESOURCE"},
				{ResourceType: "gcp-quota-slice", Env: "GCP_LEASED_RESOURCE"},
			},
		},
	}, {
		name: "invalid empty name",
		test: api.MultiStageTestConfigurationLiteral{
			Leases: []api.StepLease{{Env: "AWS_LEASED_RESOURCE"}},
		},
		err: []error{
			errors.New("test.leases[0]: 'resource_type' cannot be empty"),
		},
	}, {
		name: "invalid empty environment variable",
		test: api.MultiStageTestConfigurationLiteral{
			Leases: []api.StepLease{{ResourceType: "aws-quota-slice"}},
		},
		err: []error{
			errors.New("test.leases[0]: 'env' cannot be empty"),
		},
	}, {
		name: "invalid duplicate name",
		test: api.MultiStageTestConfigurationLiteral{
			Leases: []api.StepLease{
				{ResourceType: "aws", Env: "AWS_LEASED_RESOURCE"},
				{ResourceType: "aws", Env: "AWS_LEASED_RESOURCE"},
			},
		},
		err: []error{
			errors.New("test.leases[1]: duplicate environment variable: AWS_LEASED_RESOURCE"),
		},
	}, {
		name: "invalid duplicate name from other steps",
		test: api.MultiStageTestConfigurationLiteral{
			Leases: []api.StepLease{
				{ResourceType: "aws", Env: "AWS_LEASED_RESOURCE"},
			},
			Test: []api.LiteralTestStep{{
				As:       "as",
				From:     "from",
				Commands: "commands",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1"},
					Limits:   api.ResourceList{"memory": "1m"},
				},
				Leases: []api.StepLease{
					{ResourceType: "aws", Env: "AWS_LEASED_RESOURCE"},
				},
			}},
		},
		err: []error{
			errors.New("test.test[0].leases[0]: duplicate environment variable: AWS_LEASED_RESOURCE"),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			test := api.TestStepConfiguration{
				MultiStageTestConfigurationLiteral: &tc.test,
			}
			err := validateTestConfigurationType("test", test, nil, nil, true)
			if diff := diff.ObjectReflectDiff(tc.err, err); diff != "<no diffs>" {
				t.Errorf("unexpected error: %s", diff)
			}
		})
	}
}
