package promotion

import (
	"flag"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

func TestPromotesOfficialImages(t *testing.T) {
	var testCases = []struct {
		name       string
		configSpec *cioperatorapi.ReleaseBuildConfiguration
		expected   bool
	}{
		{
			name: "config without promotion doesn't produce official images",
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: nil,
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to ocp namespace produces official images",
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace: "ocp",
				},
			},
			expected: true,
		},
		{
			name: "config with disabled explicit promotion to ocp namespace does not produce official images",
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace: "ocp",
					Disabled:  true,
				},
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to okd namespace produces official images",
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace: "origin",
				},
			},
			expected: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := PromotesOfficialImages(testCase.configSpec, WithOKD), testCase.expected; actual != expected {
				t.Errorf("%s: did not identify official promotion correctly, expected %v got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestAllPromotionImageStreamTags(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		config   *cioperatorapi.ReleaseBuildConfiguration
		expected sets.String
	}{
		{
			name:   "nil promotionconfig",
			config: &cioperatorapi.ReleaseBuildConfiguration{},
		},
		{
			name: "disabled",
			config: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Disabled:  true,
					Namespace: "ns",
					Name:      "name",
				},
				Images: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{{To: cioperatorapi.PipelineImageStreamTagReferenceSource}},
			},
		},
		{
			name: "empty namespace",
			config: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Name: "some-stream",
				},
				Images: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{{To: cioperatorapi.PipelineImageStreamTagReferenceSource}},
			},
		},
		{
			name: "empty name",
			config: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace: "some-stream",
				},
				Images: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{{To: cioperatorapi.PipelineImageStreamTagReferenceSource}},
			},
		},
		{
			name: "images",
			config: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace: "some-namespace",
					Name:      "some-stream",
				},
				Images: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{{To: cioperatorapi.PipelineImageStreamTagReferenceSource}},
			},
			expected: sets.NewString("some-namespace/some-stream:src"),
		},
		{
			name: "additinal image",
			config: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace:        "some-namespace",
					Name:             "some-stream",
					AdditionalImages: map[string]string{"expected": ""},
				},
			},
			expected: sets.NewString("some-namespace/some-stream:expected"),
		},
		{
			name: "image and additional image",
			config: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace:        "some-namespace",
					Name:             "some-stream",
					AdditionalImages: map[string]string{"expected": ""},
				},
				Images: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{{To: cioperatorapi.PipelineImageStreamTagReferenceSource}},
			},
			expected: sets.NewString("some-namespace/some-stream:expected", "some-namespace/some-stream:src"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(AllPromotionImageStreamTags(tc.config), tc.expected); diff != "" {
				t.Errorf("result differs from expected: %s", diff)
			}
		})
	}
}

func TestDetermineReleaseBranches(t *testing.T) {
	var testCases = []struct {
		name                                         string
		currentRelease, futureRelease, currentBranch string
		expectedFutureBranch                         string
		expectedError                                bool
	}{
		{
			name:                 "promotion from weird branch errors",
			currentRelease:       "4.0",
			futureRelease:        "4.1",
			currentBranch:        "weird",
			expectedFutureBranch: "",
			expectedError:        true,
		},
		{
			name:                 "promotion from master makes a release branch",
			currentRelease:       "4.0",
			futureRelease:        "4.1",
			currentBranch:        "master",
			expectedFutureBranch: "release-4.1",
			expectedError:        false,
		},
		{
			name:                 "promotion from main makes a release branch",
			currentRelease:       "4.0",
			futureRelease:        "4.1",
			currentBranch:        "main",
			expectedFutureBranch: "release-4.1",
			expectedError:        false,
		},
		{
			name:                 "promotion from openshift release branch makes a new release branch",
			currentRelease:       "4.0",
			futureRelease:        "4.1",
			currentBranch:        "openshift-4.0",
			expectedFutureBranch: "openshift-4.1",
			expectedError:        false,
		},
		{
			name:                 "promotion from release branch for a repo ahead of the branch cut makes a new release branch",
			currentRelease:       "4.0",
			futureRelease:        "4.1",
			currentBranch:        "release-4.0",
			expectedFutureBranch: "release-4.1",
			expectedError:        false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actualFutureBranch, err := DetermineReleaseBranch(testCase.currentRelease, testCase.futureRelease, testCase.currentBranch)
			if err == nil && testCase.expectedError {
				t.Errorf("%s: expected an error, but got none", testCase.name)
			}
			if err != nil && !testCase.expectedError {
				t.Errorf("%s: expected no error, but got one: %v", testCase.name, err)
			}
			if actual, expected := actualFutureBranch, testCase.expectedFutureBranch; actual != expected {
				t.Errorf("%s: incorrect future branch, expected %q, got %q", testCase.name, expected, actual)
			}
		})
	}
}

func TestOptions_Bind(t *testing.T) {
	var testCases = []struct {
		name     string
		input    []string
		expected Options
	}{
		{
			name:  "nothing set has defaults",
			input: []string{},
			expected: Options{
				ConfirmableOptions: config.ConfirmableOptions{
					Options: config.Options{
						LogLevel: "info",
					},
				},
			},
		},
		{
			name: "everything set",
			input: []string{
				"--config-dir=foo",
				"--org=bar",
				"--repo=baz",
				"--log-level=debug",
				"--confirm",
				"--current-release=one",
				"--current-promotion-namespace=promotionns",
			},
			expected: Options{
				ConfirmableOptions: config.ConfirmableOptions{
					Options: config.Options{
						ConfigDir: "foo",
						Org:       "bar",
						Repo:      "baz",
						LogLevel:  "debug",
					},
					Confirm: true},
				CurrentRelease:            "one",
				CurrentPromotionNamespace: "promotionns",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var o Options
			fs := flag.NewFlagSet(testCase.name, flag.PanicOnError)
			o.Bind(fs)
			if err := fs.Parse(testCase.input); err != nil {
				t.Fatalf("%s: cannot parse args: %v", testCase.name, err)
			}
			if actual, expected := o, testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect options: expected %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestFutureOptions(t *testing.T) {
	var testCases = []struct {
		name               string
		input              []string
		expected           FutureOptions
		expectedFutureOpts []string
	}{
		{
			name: "everything set",
			input: []string{
				"--config-dir=foo",
				"--org=bar",
				"--repo=baz",
				"--log-level=debug",
				"--confirm",
				"--current-release=one",
				"--current-promotion-namespace=promotionns",
				"--future-release=two",
			},
			expected: FutureOptions{
				Options: Options{
					ConfirmableOptions: config.ConfirmableOptions{
						Options: config.Options{
							ConfigDir: "foo",
							Org:       "bar",
							Repo:      "baz",
							LogLevel:  "debug",
						},
						Confirm: true},
					CurrentRelease:            "one",
					CurrentPromotionNamespace: "promotionns",
				},
				FutureReleases: flagutil.Strings{},
			},
			expectedFutureOpts: []string{"two", "one"},
		},
		{
			name: "many future releases set",
			input: []string{
				"--config-dir=foo",
				"--org=bar",
				"--repo=baz",
				"--log-level=debug",
				"--confirm",
				"--current-release=one",
				"--future-release=two",
				"--future-release=three",
			},
			expected: FutureOptions{
				Options: Options{
					ConfirmableOptions: config.ConfirmableOptions{
						Options: config.Options{
							ConfigDir: "foo",
							Org:       "bar",
							Repo:      "baz",
							LogLevel:  "debug",
						},
						Confirm: true},
					CurrentRelease: "one",
				},
				FutureReleases: flagutil.Strings{},
			},
			expectedFutureOpts: []string{"two", "three", "one"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var o FutureOptions
			fs := flag.NewFlagSet(testCase.name, flag.PanicOnError)
			o.Bind(fs)
			if err := fs.Parse(testCase.input); err != nil {
				t.Fatalf("%s: cannot parse args: %v", testCase.name, err)
			}
			if err := o.Validate(); err != nil {
				t.Fatalf("%s: options did not validate: %v", testCase.name, err)
			}
			expected := testCase.expected
			// this is not exposed for testing
			for _, opt := range testCase.expectedFutureOpts {
				if err := expected.FutureReleases.Set(opt); err != nil {
					t.Errorf("failed to set future release: %v", err)
				}
			}
			if actual, expected := o, expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect options: %s", testCase.name, cmp.Diff(expected, actual, cmp.AllowUnexported(flagutil.Strings{})))
			}
		})
	}
}

func TestOptionsMatche(t *testing.T) {
	var testCases = []struct {
		name       string
		input      []string
		configSpec *cioperatorapi.ReleaseBuildConfiguration
		expected   bool
	}{
		{
			name: "promotion is disabled",
			input: []string{
				"--current-release=4.6",
			},
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Disabled:  true,
					Name:      "4.6",
					Namespace: "ocp",
				},
			},
			expected: false,
		},
		{
			name: "with default promotion namespace",
			input: []string{
				"--current-release=one",
			},
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Name:      "one",
					Namespace: "ocp",
				},
			},
			expected: true,
		},
		{
			name: "for okd4.0",
			input: []string{
				"--current-release=4.8",
			},
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Name:      "4.8",
					Namespace: "origin",
				},
			},
			expected: true,
		},
		{
			name: "with user defined promotion namespace",
			input: []string{
				"--current-release=one",
				"--current-promotion-namespace=promotionns",
			},
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Name:      "one",
					Namespace: "promotionns",
				},
			},
			expected: true,
		},
	}
	for _, testCase := range testCases {
		var o Options
		fs := flag.NewFlagSet(testCase.name, flag.PanicOnError)
		o.Bind(fs)
		if err := fs.Parse(testCase.input); err != nil {
			t.Fatalf("%s: cannot parse args: %v", testCase.name, err)
		}
		if actual, expected := o.matches(testCase.configSpec, WithOKD), testCase.expected; actual != expected {
			t.Errorf("expected matches, but failed, input_args=%v, promation_config=%v.", testCase.input, testCase.configSpec)
		}
	}
}
