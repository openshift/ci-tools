package validation

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/diff"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestValidateTests(t *testing.T) {
	cronString := "0 0 * * 1"
	invalidCronString := "r 0 * * 1"
	intervalString := "6h"
	invalidIntervalString := "6t"
	for _, tc := range []struct {
		id            string
		release       *api.ReleaseTagConfiguration
		releases      sets.Set[string]
		tests         []api.TestStepConfiguration
		resolved      bool
		expectedError error
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
			expectedError: errors.New("tests[0].as: should not be called 'images' because it gets confused with '[images]' target"),
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
			expectedError: errors.New("tests[0].as: should not begin with 'ci-index' because it gets confused with 'ci-index' and `ci-index-...` targets"),
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
			expectedError: errors.New("tests[0].as: should not begin with 'ci-index' because it gets confused with 'ci-index' and `ci-index-...` targets"),
		},
		{
			id: "No test type",
			tests: []api.TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
				},
			},
			expectedError: errors.New("tests[0] has no type, you may want to specify 'container' for a container based test"),
		},
		{
			id: "Multiple test types",
			tests: []api.TestStepConfiguration{
				{
					As:                          "test",
					Commands:                    "commands",
					ContainerTestConfiguration:  &api.ContainerTestConfiguration{},
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{},
				},
			},
			expectedError: errors.New(`tests[0] has more than one type`),
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
			expectedError: errors.New("tests[0]: `commands`, `steps`, and `literal_steps` are mutually exclusive"),
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
			expectedError: errors.New("tests[0]: 'from' is required"),
		},
		{
			id: "test without `commands`",
			tests: []api.TestStepConfiguration{
				{
					As:                         "test",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedError: errors.New("tests[0]: either `commands`, `steps`, or `literal_steps` should be set"),
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
			expectedError: errors.New(`tests[0].memory_backed_volume: 'size' must be a Kubernetes quantity: unable to parse quantity's suffix`),
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
			expectedError: errors.New("tests: found duplicated test: (test)"),
		},
		{
			id: "test without `as`",
			tests: []api.TestStepConfiguration{
				{
					Commands:                   "test",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedError: errors.New("tests[0].as: is required"),
		},
		{
			id: "with release",
			tests: []api.TestStepConfiguration{
				{
					As: "test",
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						ClusterProfile: api.ClusterProfileGCP,
					},
				},
			},
			release: &api.ReleaseTagConfiguration{Name: "origin-v4.0"},
		},
		{
			id: "invalid secret name",
			tests: []api.TestStepConfiguration{
				{
					As:                         "test",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "src"},
					Secret: &api.Secret{
						Name:      "secret_test",
						MountPath: "/path/to/secret:exec",
					},
				},
			},
			expectedError: errors.New("tests[0].name: 'secret_test' is not a valid Kubernetes object name"),
		},
		{
			id: "invalid secret and secrets both set",
			tests: []api.TestStepConfiguration{
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "src"},
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
			expectedError: errors.New("test.Secret and test.Secrets cannot both be set"),
		},
		{
			id: "invalid duplicate secret names",
			tests: []api.TestStepConfiguration{
				{
					As:                         "test",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "src"},
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
			expectedError: errors.New("duplicate secret name entries found for secret-test-a"),
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
			expectedError: errors.New(`tests[0].path: 'path/to/secret' secret mount path must be an absolute path`),
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
			expectedError: errors.New("tests[0]: non-literal test found in fully-resolved configuration"),
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
			expectedError: errors.New("tests[0]: `cron` and `postsubmit` are mututally exclusive"),
		},
		{
			id: "minimum_interval and postsubmit together are invalid",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					MinimumInterval:            &intervalString,
					Postsubmit:                 true,
				},
			},
			expectedError: errors.New("tests[0]: `minimum_interval` and `postsubmit` are mututally exclusive"),
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
		},
		{
			id: "valid minimum_interval",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					MinimumInterval:            &intervalString,
				},
			},
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
			expectedError: errors.New("tests[0]: `interval` and `cron` cannot both be set"),
		},
		{
			id: "cron and minimum_interval together are invalid",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Cron:                       &cronString,
					MinimumInterval:            &intervalString,
				},
			},
			expectedError: errors.New("tests[0]: `cron` and `minimum_interval` cannot both be set"),
		},
		{
			id: "interval and minimum_interval together are invalid",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Interval:                   &intervalString,
					MinimumInterval:            &intervalString,
				},
			},
			expectedError: errors.New("tests[0]: `interval` and `minimum_interval` cannot both be set"),
		},
		{
			id: "cron and releaseInforming together are invalid",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Cron:                       &cronString,
					ReleaseController:          true,
				},
			},
			expectedError: errors.New("tests[0]: `cron` cannot be set for release controller jobs"),
		},
		{
			id: "interval and releaseInforming together are invalid",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					ReleaseController:          true,
					Interval:                   &intervalString,
				},
			},
			expectedError: errors.New("tests[0]: `interval` cannot be set for release controller jobs"),
		},
		{
			id: "minimum_interval and releaseInforming together are invalid",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					ReleaseController:          true,
					MinimumInterval:            &intervalString,
				},
			},
			expectedError: errors.New("tests[0]: `minimum_interval` cannot be set for release controller jobs"),
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
			expectedError: errors.New("tests[0]: cannot parse cron: Failed to parse int from r: strconv.Atoi: parsing \"r\": invalid syntax"),
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
			expectedError: errors.New(`tests[0]: cannot parse interval: time: unknown unit "t" in duration "6t"`),
		},
		{
			id: "cron is mutually exclusive with run_if_changed",
			tests: []api.TestStepConfiguration{{
				As:           "unit",
				Commands:     "commands",
				Cron:         &cronString,
				RunIfChanged: "^README.md$",
			}},
			expectedError: errors.New("tests[0]: `cron`/`interval`/`minimum_interval` are mutually exclusive with `run_if_changed`/`skip_if_only_changed`/`optional`"),
		},
		{
			id: "cron can be with run_if_changed if presubmit is true",
			tests: []api.TestStepConfiguration{{
				As:                         "unit",
				Commands:                   "commands",
				Cron:                       &cronString,
				RunIfChanged:               "^(Containerfile.*|pyproject.toml|Makefile|ols/.*|scripts/.*|tests/.*|*.py)$",
				Presubmit:                  true,
				ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
			}},
		},
		{
			id: "cron is mutually exclusive with skip_if_only_changed",
			tests: []api.TestStepConfiguration{{
				As:                         "unit",
				Commands:                   "commands",
				Cron:                       &cronString,
				SkipIfOnlyChanged:          "^README.md$",
				ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
			}},
			expectedError: errors.New("tests[0]: `cron`/`interval`/`minimum_interval` are mutually exclusive with `run_if_changed`/`skip_if_only_changed`/`optional`"),
		},
		{
			id: "cron can be with skip_if_not_changed if presubmit is true",
			tests: []api.TestStepConfiguration{{
				As:                         "unit",
				Commands:                   "commands",
				Cron:                       &cronString,
				SkipIfOnlyChanged:          "^README.md$",
				Presubmit:                  true,
				ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
			}},
		},
		{
			id: "interval is mutually exclusive with run_if_changed",
			tests: []api.TestStepConfiguration{{
				As:           "unit",
				Commands:     "commands",
				Interval:     &intervalString,
				RunIfChanged: "^README.md$",
			}},
			expectedError: errors.New("tests[0]: `cron`/`interval`/`minimum_interval` are mutually exclusive with `run_if_changed`/`skip_if_only_changed`/`optional`"),
		},
		{
			id: "minimum_interval is mutually exclusive with run_if_changed",
			tests: []api.TestStepConfiguration{{
				As:              "unit",
				Commands:        "commands",
				MinimumInterval: &intervalString,
				RunIfChanged:    "^README.md$",
			}},
			expectedError: errors.New("tests[0]: `cron`/`interval`/`minimum_interval` are mutually exclusive with `run_if_changed`/`skip_if_only_changed`/`optional`"),
		},
		{
			id: "Run if changed and skip_if_only_changed are mutually exclusive",
			tests: []api.TestStepConfiguration{{
				As:                "unit",
				Commands:          "commands",
				RunIfChanged:      "^README.md$",
				SkipIfOnlyChanged: "^OTHER_README.md$",
			}},
			expectedError: errors.New("tests[0]: `run_if_changed` and `skip_if_only_changed` are mutually exclusive"),
		},
		{
			id: "secrets used on multi-stage tests",
			tests: []api.TestStepConfiguration{{
				As:                          "unit",
				Secrets:                     []*api.Secret{{Name: "secret"}},
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{},
			}},
			expectedError: errors.New("tests[0]: secret/secrets can be only used with container-based tests (use credentials in multi-stage tests)"),
		},
		{
			id: "cron is mutually exclusive with optional",
			tests: []api.TestStepConfiguration{{
				As:       "unit",
				Commands: "commands",
				Cron:     &cronString,
				Optional: true,
			}},
			expectedError: errors.New("tests[0]: `cron`/`interval`/`minimum_interval` are mutually exclusive with `run_if_changed`/`skip_if_only_changed`/`optional`"),
		},
		{
			id: "interval is mutually exclusive with optional",
			tests: []api.TestStepConfiguration{{
				As:       "unit",
				Commands: "commands",
				Interval: &intervalString,
				Optional: true,
			}},
			expectedError: errors.New("tests[0]: `cron`/`interval`/`minimum_interval` are mutually exclusive with `run_if_changed`/`skip_if_only_changed`/`optional`"),
		},
		{
			id: "minimum_interval is mutually exclusive with optional",
			tests: []api.TestStepConfiguration{{
				As:              "unit",
				Commands:        "commands",
				MinimumInterval: &intervalString,
				Optional:        true,
			}},
			expectedError: errors.New("tests[0]: `cron`/`interval`/`minimum_interval` are mutually exclusive with `run_if_changed`/`skip_if_only_changed`/`optional`"),
		},
		{
			id: "postsubmit job is mutually exclusive with optional",
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Optional:                   true,
					Postsubmit:                 true,
				},
			},
			expectedError: errors.New("tests[0]: `optional` and `postsubmit` are mututally exclusive"),
		},
		{
			id: "test name too long",
			tests: []api.TestStepConfiguration{
				{
					As:                         "yada-yada-yada-yada-yada-yada-yada-yada-yada-yada-yada-yada-yada-yada-yada-yada-yada-yada",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedError: errors.New("tests[0].as: 89 characters long, maximum length is 61"),
		},
		{
			id: "test name too long for claim tests which must be shorter",
			tests: []api.TestStepConfiguration{
				{
					As: "yada-yada-yada-yada-yada-yada-yada-yada-yada-yada",
					ClusterClaim: &api.ClusterClaim{
						Version: "4.9",
						Cloud:   "gcp",
						Owner:   "ME",
					},
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{},
				},
			},
			expectedError: errors.New("tests[0].as: 49 characters long, maximum length is 42 for tests with claims"),
		},
		{
			id: `presbumit cannot be true in a presubmit`,
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Presubmit:                  true,
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedError: errors.New("tests[0]: `presubmit` can be used only for periodics"),
		},
		{
			id: `presbumit cannot be true in a postsubmit`,
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Presubmit:                  true,
					Postsubmit:                 true,
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
				},
			},
			expectedError: errors.New("tests[0]: `presubmit` can be used only for periodics"),
		},
		{
			id: `presbumit can be true in a periodic`,
			tests: []api.TestStepConfiguration{
				{
					As:                         "unit",
					Commands:                   "commands",
					ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
					Cron:                       &cronString,
					Presubmit:                  true,
				},
			},
		},
	} {
		t.Run(tc.id, func(t *testing.T) {
			v := newSingleUseValidator()
			errs := v.validateTestStepConfiguration(NewConfigContext(), "tests", tc.tests, tc.release, nil, tc.releases, sets.New[string](), tc.resolved)
			if tc.expectedError == nil && len(errs) > 0 {
				t.Errorf("expected to be valid, got: %v", errs)
			}
			if tc.expectedError != nil {
				var found bool
				for _, err := range errs {
					if cmp.Diff(err.Error(), tc.expectedError.Error(), testhelper.EquateErrorMessage) == "" {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected '%v' error to be present in:\n%v", tc.expectedError, errs)
				}
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
	defaultDuration := &prowv1.Duration{Duration: 1 * time.Minute}
	for _, tc := range []struct {
		name         string
		steps        []api.TestStep
		seen         sets.Set[string]
		errs         []error
		releases     sets.Set[string]
		clusterClaim api.ClaimRelease
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
		seen: sets.New[string]("s0"),
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
		releases: sets.New[string]("previous"),
	}, {
		name: "invalid image 4",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "as",
				From:      "stable-nonexistent:base",
				Commands:  "commands",
				Resources: resources},
		}},
		releases: sets.New[string]("previous"),
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
	}, {
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:          "trapper-keeper",
				From:        "installer",
				Commands:    `trap "echo Aw Snap!" SIGINT SIGTERM`,
				GracePeriod: defaultDuration,
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				}},
		}},
	}, {
		name: "Workflow with trap command without grace_period",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:       "trapper-keeper",
				From:     "installer",
				Commands: `trap "echo Aw Snap!" SIGINT SIGTERM`,
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				}},
		}},
		errs: []error{
			errors.New("test `trapper-keeper` has `commands` containing `trap` command, but test step is missing grace_period"),
		},
	}, {
		name: "Workflow with best effort with timeout",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:         "best-effort",
				From:       "installer",
				Commands:   `openshift-cluster install`,
				BestEffort: &yes,
				Timeout:    defaultDuration,
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				}},
		}},
	}, {
		name: "Workflow with best effort without timeout",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:         "best-effort",
				From:       "installer",
				Commands:   "openshift-cluster install",
				BestEffort: &yes,
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m"},
					Limits:   api.ResourceList{"memory": "2Gi"},
				}},
		}},
		errs: []error{
			errors.New("test best-effort contains best_effort without timeout"),
		},
	}, {
		name: "cluster claim release",
		steps: []api.TestStep{{
			LiteralTestStep: &api.LiteralTestStep{
				As:        "as",
				From:      "stable-myclaim:base",
				Commands:  "commands",
				Resources: resources},
		}},
		clusterClaim: api.ClaimRelease{ReleaseName: "myclaim-as", OverrideName: "myclaim"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			context := newContext("test", nil, tc.releases, make(testInputImages))
			if tc.seen != nil {
				context.namesSeen = tc.seen
			}
			v := NewValidator(nil, nil)
			ret := v.validateTestSteps(context, testStageTest, tc.steps, &tc.clusterClaim)
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
		seen     sets.Set[string]
		errs     []error
		releases sets.Set[string]
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
			context := newContext("test", nil, tc.releases, make(testInputImages))
			if tc.seen != nil {
				context.namesSeen = tc.seen
			}
			v := NewValidator(nil, nil)
			ret := v.validateTestSteps(context, testStagePost, tc.steps, nil)
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
		releases sets.Set[string]
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
			v := NewValidator(nil, nil)
			err := v.validateLiteralTestStep(newContext("test", tc.env, tc.releases, make(testInputImages)), testStageTest, api.LiteralTestStep{
				As:       "as",
				From:     "from",
				Commands: "commands",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1"},
					Limits:   api.ResourceList{"memory": "1m"},
				},
				Environment: tc.params,
			}, nil)
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
				{Namespace: "ns", Name: "name", MountPath: "/foo", Collection: "1"},
				{Namespace: "ns", Name: "name", MountPath: "/foo", Collection: "2"},
			},
			output: []error{
				errors.New("root.credentials[0] and credentials[1] mount to the same location (/foo) and have the same name, which would result in a collision"),
			},
		},
		{
			name: "duped cred mount path is ok if in the same collection",
			input: []api.CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/foo", Collection: "1"},
				{Namespace: "ns", Name: "different-name", MountPath: "/foo", Collection: "1"},
			},
		},
		{
			name: "duped cred name is ok if different mount path",
			input: []api.CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/foo", Collection: "1"},
				{Namespace: "ns", Name: "name", MountPath: "/bar", Collection: "1"},
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

func TestValidateDNSConfig(t *testing.T) {
	var testCases = []struct {
		name   string
		input  []api.StepDNSConfig
		output []error
	}{
		{
			name: "no searches",
		},
		{
			name: "valid searches",
			input: []api.StepDNSConfig{
				{Nameservers: []string{""}, Searches: []string{"search1", "search2"}},
				{Nameservers: []string{"Nameserver1"}, Searches: []string{"search1", "search2"}},
			},
		},
		{
			name: "invalid searches",
			input: []api.StepDNSConfig{
				{Nameservers: []string{"nameserver1"}, Searches: []string{"", ""}},
				{Nameservers: []string{""}, Searches: []string{"", ""}},
			},
			output: []error{
				errors.New("root.searches[0] must be set"),
				errors.New("root.searches[1] must be set"),
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateDNSConfig("root", testCase.input)
			if diff := cmp.Diff(err, testCase.output, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
		})
	}
}

func TestValidateNodeArchitecture(t *testing.T) {
	var testCases = []struct {
		name   string
		input  api.NodeArchitecture
		output error
	}{
		{
			name:   "empty Node Architecture",
			output: errors.New("root.nodeArchitecture: invalid node architecture , expected one of amd64 or arm64"),
		},
		{
			name:  "valid AMD64 Node Architecture",
			input: api.NodeArchitectureAMD64,
		},
		{
			name:  "valid ARM64 Node Architecture",
			input: api.NodeArchitectureARM64,
		},
		{
			name:   "invalid Node Architecture",
			input:  api.NodeArchitecture("s390x"),
			output: errors.New("root.nodeArchitecture: invalid node architecture s390x, expected one of amd64 or arm64"),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateNodeArchitecture("root", testCase.input)
			if diff := cmp.Diff(err, testCase.output, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
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
			errors.New("tests[0].steps.leases[0]: 'resource_type' cannot be empty"),
		},
	}, {
		name: "invalid empty environment variable",
		test: api.MultiStageTestConfigurationLiteral{
			Leases: []api.StepLease{{ResourceType: "aws-quota-slice"}},
		},
		err: []error{
			errors.New("tests[0].steps.leases[0]: 'env' cannot be empty"),
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
			errors.New("tests[0].steps.leases[1]: duplicate environment variable: AWS_LEASED_RESOURCE"),
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
			errors.New("tests[0].steps.test[0].leases[0]: duplicate environment variable: AWS_LEASED_RESOURCE"),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			test := api.TestStepConfiguration{
				MultiStageTestConfigurationLiteral: &tc.test,
			}
			v := NewValidator(nil, nil)
			err := v.validateTestConfigurationType("tests[0]", test, nil, nil, nil, make(testInputImages), true)
			if diff := diff.ObjectReflectDiff(tc.err, err); diff != "<no diffs>" {
				t.Errorf("unexpected error: %s", diff)
			}
		})
	}
}

func TestValidateTestConfigurationType(t *testing.T) {
	for _, tc := range []struct {
		name     string
		test     api.TestStepConfiguration
		expected []error
	}{
		{
			name: "valid claim",
			test: api.TestStepConfiguration{
				ClusterClaim: &api.ClusterClaim{
					Product:      api.ReleaseProductOCP,
					Version:      "4.6.0",
					Architecture: api.ReleaseArchitectureAMD64,
					Cloud:        api.CloudAWS,
					Owner:        "dpp",
					Timeout:      &prowv1.Duration{Duration: time.Hour},
					Labels:       map[string]string{"size": "2", "multi-az": "false"},
				},
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					Test: []api.TestStep{
						{
							LiteralTestStep: &api.LiteralTestStep{
								As:        "e2e-aws-test",
								Commands:  "oc get node",
								From:      "cli",
								Resources: api.ResourceRequirements{Requests: api.ResourceList{"cpu": "1"}},
							},
						},
					},
				},
			},
		},
		{
			name: "claim and cluster_profile",
			test: api.TestStepConfiguration{
				ClusterClaim: &api.ClusterClaim{
					Product:      api.ReleaseProductOCP,
					Version:      "4.6.0",
					Architecture: api.ReleaseArchitectureAMD64,
					Cloud:        api.CloudAWS,
					Owner:        "dpp",
					Timeout:      &prowv1.Duration{Duration: time.Hour},
				},
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					ClusterProfile: api.ClusterProfileAWS,
					Test: []api.TestStep{
						{
							LiteralTestStep: &api.LiteralTestStep{
								As:        "e2e-aws-test",
								Commands:  "oc get node",
								From:      "cli",
								Resources: api.ResourceRequirements{Requests: api.ResourceList{"cpu": "1"}},
							},
						},
					},
				},
			},
			expected: []error{fmt.Errorf("test installs more than one cluster, probably it defined both cluster_claim and cluster_profile")},
		},
		{
			name: "claim missing fields",
			test: api.TestStepConfiguration{
				ClusterClaim: &api.ClusterClaim{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
					Timeout:      &prowv1.Duration{Duration: time.Hour},
				},
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					Test: []api.TestStep{
						{
							LiteralTestStep: &api.LiteralTestStep{
								As:        "e2e-aws-test",
								Commands:  "oc get node",
								From:      "cli",
								Resources: api.ResourceRequirements{Requests: api.ResourceList{"cpu": "1"}},
							},
						},
					},
				},
			},
			expected: []error{fmt.Errorf("test.cluster_claim.version cannot be empty when cluster_claim is not nil"),
				fmt.Errorf("test.cluster_claim.cloud cannot be empty when cluster_claim is not nil"),
				fmt.Errorf("test.cluster_claim.owner cannot be empty when cluster_claim is not nil")},
		},
		{
			name: "valid cluster",
			test: api.TestStepConfiguration{
				Cluster: api.ClusterBuild01,
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					Test: []api.TestStep{
						{
							LiteralTestStep: &api.LiteralTestStep{
								As:        "e2e-aws-test",
								Commands:  "oc get node",
								From:      "cli",
								Resources: api.ResourceRequirements{Requests: api.ResourceList{"cpu": "1"}},
							},
						},
					},
				},
			},
		},
		{
			name: "invalid cluster",
			test: api.TestStepConfiguration{
				Cluster: "bar",
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					Test: []api.TestStep{
						{
							LiteralTestStep: &api.LiteralTestStep{
								As:        "e2e-aws-test",
								Commands:  "oc get node",
								From:      "cli",
								Resources: api.ResourceRequirements{Requests: api.ResourceList{"cpu": "1"}},
							},
						},
					},
				},
			},
			expected: []error{fmt.Errorf("test.cluster is not a valid cluster: bar")},
		},
		{
			name: "claim on a container test -> error",
			test: api.TestStepConfiguration{
				ContainerTestConfiguration: &api.ContainerTestConfiguration{
					From: "src",
				},
				ClusterClaim: &api.ClusterClaim{
					Product:      api.ReleaseProductOCP,
					Version:      "4.6.0",
					Architecture: api.ReleaseArchitectureAMD64,
					Cloud:        api.CloudAWS,
					Owner:        "dpp",
					Timeout:      &prowv1.Duration{Duration: time.Hour},
				},
			},
			expected: []error{errors.New("test.cluster_claim cannot be set on a test which is not a multi-stage test")},
		},
		{
			name: "claim with a built-in key -> error",
			test: api.TestStepConfiguration{
				ClusterClaim: &api.ClusterClaim{
					Product:      api.ReleaseProductOCP,
					Version:      "4.6.0",
					Architecture: api.ReleaseArchitectureAMD64,
					Cloud:        api.CloudAWS,
					Owner:        "dpp",
					Timeout:      &prowv1.Duration{Duration: time.Hour},
					Labels:       map[string]string{"cloud": "b"},
				},
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					Test: []api.TestStep{
						{
							LiteralTestStep: &api.LiteralTestStep{
								As:        "e2e-aws-test",
								Commands:  "oc get node",
								From:      "cli",
								Resources: api.ResourceRequirements{Requests: api.ResourceList{"cpu": "1"}},
							},
						},
					},
				},
			},
			expected: []error{
				errors.New("test.cluster_claim.labels contains an invalid key in claim's label: cloud"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := NewValidator(nil, nil)
			actual := v.validateTestConfigurationType("test", tc.test, nil, nil, nil, make(testInputImages), false)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("expected differs from actual: %s", diff)
			}
		})
	}
}

func TestVerifyClusterProfileOwnership(t *testing.T) {
	cpMap := api.ClusterProfilesMap{
		"profile-with-one-owner": api.ClusterProfileDetails{
			Profile: "profile-with-one-owner",
			Owners: []api.ClusterProfileOwners{
				{
					Org: "org",
				},
			},
		},
		"profile-with-one-owner-w-multiple-repos": api.ClusterProfileDetails{
			Profile: "profile-with-one-owner-w-multiple-repos",
			Owners: []api.ClusterProfileOwners{
				{
					Org:   "org2",
					Repos: []string{"repo21", "repo22"},
				},
			},
		},
		"profile-with-multiple-orgs-and-repos": api.ClusterProfileDetails{
			Profile: "profile-with-multiple-orgs-and-repos",
			Owners: []api.ClusterProfileOwners{
				{
					Org:   "org1",
					Repos: []string{"repo1"},
				},
				{
					Org:   "org2",
					Repos: []string{"repo21", "repo22"},
				},
				{
					Org: "org3",
				},
			},
		},
		"profile-with-no-owners-specified": api.ClusterProfileDetails{
			Profile: "profile-with-no-owners-specified",
			Owners:  []api.ClusterProfileOwners{},
		},
	}
	v := NewValidator(cpMap, nil)

	for _, tc := range []struct {
		name     string
		profile  api.ClusterProfileDetails
		metadata *api.Metadata
		expected error
	}{
		{
			name:    "ownership not restricted",
			profile: v.validClusterProfiles["profile-with-no-owners-specified"],
			metadata: &api.Metadata{
				Org:  "any-org",
				Repo: "any-repo",
			},
		},
		{
			name:    "not one of owners",
			profile: v.validClusterProfiles["profile-with-one-owner"],
			metadata: &api.Metadata{
				Org:  "wrong-org",
				Repo: "any-repo",
			},
			expected: fmt.Errorf("wrong-org/any-repo is not an owner of the cluster profile: \"profile-with-one-owner\""),
		},
		{
			name:    "basic ok case",
			profile: v.validClusterProfiles["profile-with-one-owner"],
			metadata: &api.Metadata{
				Org:  "org",
				Repo: "any-repo",
			},
		},
		{
			name:    "complex case ok",
			profile: v.validClusterProfiles["profile-with-multiple-orgs-and-repos"],
			metadata: &api.Metadata{
				Org:  "org2",
				Repo: "repo22",
			},
		},
		{
			name:    "complex case ok - no repos",
			profile: v.validClusterProfiles["profile-with-multiple-orgs-and-repos"],
			metadata: &api.Metadata{
				Org:  "org3",
				Repo: "any-repo",
			},
		},
		{
			name:    "complex case nok",
			profile: v.validClusterProfiles["profile-with-multiple-orgs-and-repos"],
			metadata: &api.Metadata{
				Org:  "org2",
				Repo: "wrong-repo",
			},
			expected: fmt.Errorf("org2/wrong-repo is not an owner of the cluster profile: \"profile-with-multiple-orgs-and-repos\""),
		},
		{
			name:    "missing metadata - empty",
			profile: v.validClusterProfiles["profile-with-multiple-orgs-and-repos"],
			metadata: &api.Metadata{
				Org:  "",
				Repo: "",
			},
			expected: fmt.Errorf("can't do ownership check, metadata not defined"),
		},
		{
			name:     "missing metadata - nil",
			profile:  v.validClusterProfiles["profile-with-multiple-orgs-and-repos"],
			metadata: nil,
			expected: fmt.Errorf("can't do ownership check, metadata not defined"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual := verifyClusterProfileOwnership(tc.profile, tc.metadata)
			if d := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); d != "" {
				t.Errorf("expected differs from actual: %s\n", d)
			}
		})
	}
}

func TestVerifyClusterClaimOwnership(t *testing.T) {
	clusterClaim := api.ClusterClaimOwnersMap{
		"claim-with-one-owner": api.ClusterClaimDetails{
			Claim: "claim-with-one-owner",
			Owners: []api.ClusterClaimOwnerDetails{
				{
					Org: "org",
				},
			},
		},
		"claim-with-one-owner-w-multiple-repos": api.ClusterClaimDetails{
			Claim: "claim-with-one-owner-w-multiple-repos",
			Owners: []api.ClusterClaimOwnerDetails{
				{
					Org:   "org2",
					Repos: []string{"repo21", "repo22"},
				},
			},
		},
		"claim-with-multiple-orgs-and-repos": api.ClusterClaimDetails{
			Claim: "claim-with-multiple-orgs-and-repos",
			Owners: []api.ClusterClaimOwnerDetails{
				{
					Org:   "org1",
					Repos: []string{"repo1"},
				},
				{
					Org:   "org2",
					Repos: []string{"repo21", "repo22"},
				},
				{
					Org: "org3",
				},
			},
		},
		"claim-with-no-owners-specified": api.ClusterClaimDetails{
			Claim:  "claim-with-no-owners-specified",
			Owners: []api.ClusterClaimOwnerDetails{},
		},
	}
	v := NewValidator(nil, clusterClaim)

	for _, tc := range []struct {
		name     string
		claim    api.ClusterClaimDetails
		metadata *api.Metadata
		expected error
	}{
		{
			name:  "ownership not restricted",
			claim: v.validClusterClaimOwners["claim-with-no-owners-specified"],
			metadata: &api.Metadata{
				Org:  "any-org",
				Repo: "any-repo",
			},
		},
		{
			name:  "not one of owners",
			claim: v.validClusterClaimOwners["claim-with-one-owner"],
			metadata: &api.Metadata{
				Org:  "wrong-org",
				Repo: "any-repo",
			},
			expected: fmt.Errorf("wrong-org/any-repo is not an owner of the cluster claim: \"claim-with-one-owner\""),
		},
		{
			name:  "basic ok case",
			claim: v.validClusterClaimOwners["claim-with-one-owner"],
			metadata: &api.Metadata{
				Org:  "org",
				Repo: "any-repo",
			},
		},
		{
			name:  "complex case ok",
			claim: v.validClusterClaimOwners["claim-with-multiple-orgs-and-repos"],
			metadata: &api.Metadata{
				Org:  "org2",
				Repo: "repo22",
			},
		},
		{
			name:  "complex case ok - no repos",
			claim: v.validClusterClaimOwners["claim-with-multiple-orgs-and-repos"],
			metadata: &api.Metadata{
				Org:  "org3",
				Repo: "any-repo",
			},
		},
		{
			name:  "complex case nok",
			claim: v.validClusterClaimOwners["claim-with-multiple-orgs-and-repos"],
			metadata: &api.Metadata{
				Org:  "org2",
				Repo: "wrong-repo",
			},
			expected: fmt.Errorf("org2/wrong-repo is not an owner of the cluster claim: \"claim-with-multiple-orgs-and-repos\""),
		},
		{
			name:  "missing metadata - empty",
			claim: v.validClusterClaimOwners["claim-with-multiple-orgs-and-repos"],
			metadata: &api.Metadata{
				Org:  "",
				Repo: "",
			},
			expected: fmt.Errorf("can't do ownership check, metadata not defined"),
		},
		{
			name:     "missing metadata - nil",
			claim:    v.validClusterClaimOwners["claim-with-multiple-orgs-and-repos"],
			metadata: nil,
			expected: fmt.Errorf("can't do ownership check, metadata not defined"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual := verifyClusterClaimOwnership(tc.claim, tc.metadata)
			if d := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); d != "" {
				t.Errorf("expected differs from actual: %s\n", d)
			}
		})
	}
}
