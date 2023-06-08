package api

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestPromotesOfficialImages(t *testing.T) {
	var testCases = []struct {
		name       string
		configSpec *ReleaseBuildConfiguration
		expected   bool
	}{
		{
			name: "config without promotion doesn't produce official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: nil,
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to ocp namespace produces official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Namespace: "ocp",
				},
			},
			expected: true,
		},
		{
			name: "config with disabled explicit promotion to ocp namespace does not produce official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Namespace: "ocp",
					Disabled:  true,
				},
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to okd namespace produces official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
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

func TestTagsInQuay(t *testing.T) {
	var testCases = []struct {
		name        string
		image       string
		tag         ImageStreamTagReference
		date        string
		expected    []string
		expectedErr error
	}{
		{
			name:     "basic case",
			image:    "docker-registry.default.svc:5000/ci-op-bgqwwknr/pipeline@sha256:d8385fb539f471d4f41da131366b559bb90eeeeca2edd265e10d7c2aa052a1af",
			tag:      ImageStreamTagReference{Namespace: "ci", Name: "ci-operator", Tag: "latest"},
			date:     "20230605",
			expected: []string{"quay.io/openshift/ci:20230605_sha256_d8385fb539f471d4f41da131366b559bb90eeeeca2edd265e10d7c2aa052a1af", "quay.io/openshift/ci:ci_ci-operator_latest"},
		},
		{
			name:        "malformed image pull spec",
			image:       "some.io/org/repo:tag",
			date:        "20230605",
			expectedErr: fmt.Errorf("malformed image pull spec: some.io/org/repo:tag"),
		},
		{
			name:        "date must not be empty",
			expectedErr: fmt.Errorf("date must not be empty"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, actualErr := tagsInQuay(testCase.image, testCase.tag, testCase.date)
			if diff := cmp.Diff(testCase.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: mismatch (-expected +actual), diff: %s", testCase.name, diff)
			}
			if actualErr == nil {
				if diff := cmp.Diff(testCase.expected, actual); diff != "" {
					t.Errorf("%s: mismatch (-expected +actual), diff: %s", testCase.name, diff)
				}
			}
		})
	}
}
