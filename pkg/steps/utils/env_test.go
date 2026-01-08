package utils

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestEscaping(t *testing.T) {
	var testCases = []struct {
		unescaped, escaped string
	}{
		{
			unescaped: "cluster-api-operator",
			escaped:   "CLUSTER_API_OPERATOR",
		},
		{
			unescaped: "installer",
			escaped:   "INSTALLER",
		},
	}

	for _, testCase := range testCases {
		var testPlans = []struct {
			action          string
			work            func(string) string
			input, expected string
		}{
			{
				action:   "escaping",
				work:     escapedImageName,
				input:    testCase.unescaped,
				expected: testCase.escaped,
			},
			{
				action:   "unescaping",
				work:     unescapedImageName,
				input:    testCase.escaped,
				expected: testCase.unescaped,
			},
			{
				action: "round-tripping",
				work: func(s string) string {
					return unescapedImageName(escapedImageName(s))
				},
				input:    testCase.unescaped,
				expected: testCase.unescaped,
			},
		}
		for _, test := range testPlans {
			actual := test.work(test.input)
			if actual != test.expected {
				t.Errorf("%s did not yield %s, got %s", test.action, test.expected, actual)
			}
		}
	}
}

func TestLinkForEnv(t *testing.T) {
	var testCases = []struct {
		input  string
		output api.StepLink
		valid  bool
	}{
		{
			input:  "unrelated",
			output: nil,
			valid:  false,
		},
		{
			input:  "IMAGE_FORMAT",
			output: api.ImagesReadyLink(),
			valid:  true,
		},
		{
			input:  "LOCAL_IMAGE_COMPONENT",
			output: api.InternalImageLink("component"),
			valid:  true,
		},
		{
			input:  "IMAGE_COMPONENT",
			output: api.ReleaseImagesLink(api.LatestReleaseName),
			valid:  true,
		},
		{
			input:  "INITIAL_IMAGE_COMPONENT",
			output: api.ReleaseImagesLink(api.InitialReleaseName),
			valid:  true,
		},
		{
			input:  "RELEASE_IMAGE_FOOBAR",
			output: api.ReleasePayloadImageLink("foobar"),
			valid:  true,
		},
	}

	for _, testCase := range testCases {
		link, valid := LinkForEnv(testCase.input)
		if valid != testCase.valid {
			t.Errorf("didn't determine env validity correctly for %q, expected %v", testCase.input, testCase.valid)
		}
		if diff := cmp.Diff(link, testCase.output, api.Comparer()); diff != "" {
			t.Errorf("got incorrect link for %q: %v", testCase.input, diff)
		}
	}
}

func TestEnvVarFor(t *testing.T) {
	var testCases = []struct {
		input, expected string
		work            func(string) string
		check           func(string) bool
		revert          func(string) string
	}{
		{
			input:    "src",
			expected: "LOCAL_IMAGE_SRC",
			work: func(s string) string {
				return PipelineImageEnvFor(api.PipelineImageStreamTagReference(s))
			},
			check: IsPipelineImageEnv,
		},
		{
			input:    "cluster-actuator-thing-operator-stuff",
			expected: "IMAGE_CLUSTER_ACTUATOR_THING_OPERATOR_STUFF",
			work:     StableImageEnv,
			check:    IsStableImageEnv,
			revert:   StableImageNameFrom,
		},
		{
			input:    "whatever",
			expected: "INITIAL_IMAGE_WHATEVER",
			work:     InitialImageEnv,
			check:    IsInitialImageEnv,
		},
		{
			input:    "useful",
			expected: "RELEASE_IMAGE_USEFUL",
			work:     ReleaseImageEnv,
			check:    IsReleaseImageEnv,
			revert:   ReleaseNameFrom,
		},
	}

	for _, testCase := range testCases {
		actual := testCase.work(testCase.input)
		if actual != testCase.expected {
			t.Errorf("got incorrect env %q for %q, wanted %q", actual, testCase.input, testCase.expected)
		}
		if !testCase.check(actual) {
			t.Errorf("check did not pass for %q created from %q", actual, testCase.input)
		}
		if testCase.revert != nil {
			reverted := testCase.revert(actual)
			if reverted != testCase.input {
				t.Errorf("failed to round-trip: %q -> %q -> %q", testCase.input, actual, reverted)
			}
		}
	}
}

func TestGetOverriddenImages(t *testing.T) {
	testCases := []struct {
		name     string
		env      map[string]string
		expected map[string]string
	}{
		{
			name: "image overridden",
			env: map[string]string{
				OverrideImageEnv("machine-os-content"): "some-name",
			},
			expected: map[string]string{
				"machine-os-content": "some-name",
			},
		},
		{
			name: "no overrides configured",
		},
		{
			name: "multiple images overridden",
			env: map[string]string{
				OverrideImageEnv("machine-os-content"): "some-name",
				OverrideImageEnv("telemeter"):          "some-other-name",
			},
			expected: map[string]string{
				"machine-os-content": "some-name",
				"telemeter":          "some-other-name",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			images := GetOverriddenImages()
			if diff := cmp.Diff(images, tc.expected, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("returned images do not match expected, diff: %s", diff)
			}
		})
	}
}

func TestGetOpenshiftInstallerEnvVars(t *testing.T) {
	testCases := []struct {
		name     string
		env      map[string]string
		expected map[string]string
	}{
		{
			name: "OPENSHIFT_INSTALL_AWS_PUBLIC_ONLY is passed through",
			env: map[string]string{
				"OPENSHIFT_INSTALL_AWS_PUBLIC_ONLY": "true",
			},
			expected: map[string]string{
				"OPENSHIFT_INSTALL_AWS_PUBLIC_ONLY": "true",
			},
		},
		{
			name:     "no OPENSHIFT_INSTALL_* vars configured",
			expected: nil,
		},
		{
			name: "multiple OPENSHIFT_INSTALL_* vars",
			env: map[string]string{
				"OPENSHIFT_INSTALL_AWS_PUBLIC_ONLY":        "true",
				"OPENSHIFT_INSTALL_PRESERVE_BOOTSTRAP":     "true",
				"OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE": "registry.example.com/ocp:4.22",
			},
			expected: map[string]string{
				"OPENSHIFT_INSTALL_AWS_PUBLIC_ONLY":        "true",
				"OPENSHIFT_INSTALL_PRESERVE_BOOTSTRAP":     "true",
				"OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE": "registry.example.com/ocp:4.22",
			},
		},
		{
			name: "non-OPENSHIFT_INSTALL_* vars are ignored",
			env: map[string]string{
				"OPENSHIFT_INSTALL_AWS_PUBLIC_ONLY": "true",
				"OTHER_VAR":                         "ignored",
				"OPENSHIFT_OTHER":                   "also-ignored",
			},
			expected: map[string]string{
				"OPENSHIFT_INSTALL_AWS_PUBLIC_ONLY": "true",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			envVars := GetOpenshiftInstallerEnvVars()
			if diff := cmp.Diff(envVars, tc.expected, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("returned env vars do not match expected, diff: %s", diff)
			}
		})
	}
}
