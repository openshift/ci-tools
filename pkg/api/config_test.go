package api

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestWithPresubmitFrom(t *testing.T) {
	baseReleaseTagConfiguration := ReleaseTagConfiguration{Namespace: "base-namespace", Name: "base-is"}
	sourceReleaseTagConfiguration := ReleaseTagConfiguration{Namespace: "source-namespace", Name: "source-is"}
	baseTest := TestStepConfiguration{As: "base-test"}
	sourceTest := TestStepConfiguration{As: "source-test"}
	baseBaseImages := map[string]ImageStreamTagReference{"base-image": {
		Namespace: "base-namespace",
		Name:      "base-image",
		Tag:       "base-tag",
	}}
	sourceBaseImages := map[string]ImageStreamTagReference{"source-image": {
		Namespace: "source-namespace",
		Name:      "source-image",
		Tag:       "source-tag",
	}}
	baseImage := ProjectDirectoryImageBuildStepConfiguration{From: "base-image", To: "some-image"}
	sourceImage := ProjectDirectoryImageBuildStepConfiguration{From: "source-image", To: "other-image"}

	testCases := []struct {
		name   string
		base   *ReleaseBuildConfiguration
		source *ReleaseBuildConfiguration
		test   string

		// this is a shortcut to avoid repeating standard source/expected output
		// tests for testcases that check struct members unrelated to tests
		defaultTests bool

		expected      *ReleaseBuildConfiguration
		expectedError error
	}{
		{
			name:     "selected test from source is present in result",
			base:     &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:   &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
			expected: &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
		},
		{
			name:          "error when selected test is not found in source",
			test:          "nonexistent",
			base:          &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:        &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
			expectedError: errors.New("test 'nonexistent' not found in source configuration"),
		},
		{
			name:     "selected test from source is present in result with interval stripped",
			base:     &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:   &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{{As: "source-test", Interval: pointer.StringPtr("24h")}}},
			expected: &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
		},
		{
			name:     "selected test from source is present in result with cron stripped",
			base:     &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:   &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{{As: "source-test", Cron: pointer.StringPtr("@hourly")}}},
			expected: &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
		},
		{
			name:     "selected test from source is present in result with postsubmit stripped away",
			base:     &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:   &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{{As: "source-test", Postsubmit: true}}},
			expected: &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
		},
		{
			name:         "tag_specification from base is kept",
			base:         &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &baseReleaseTagConfiguration}},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &sourceReleaseTagConfiguration}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &baseReleaseTagConfiguration}},
			defaultTests: true,
		},
		{
			name:         "images from base is kept",
			base:         &ReleaseBuildConfiguration{Images: []ProjectDirectoryImageBuildStepConfiguration{baseImage}},
			source:       &ReleaseBuildConfiguration{Images: []ProjectDirectoryImageBuildStepConfiguration{sourceImage}},
			expected:     &ReleaseBuildConfiguration{Images: []ProjectDirectoryImageBuildStepConfiguration{baseImage}},
			defaultTests: true,
		},
		{
			name: "build_root from base is kept",
			base: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BuildRootImage: &BuildRootImageConfiguration{FromRepository: true}}},
			source: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BuildRootImage: &BuildRootImageConfiguration{
				ImageStreamTagReference: &ImageStreamTagReference{Namespace: "source", Name: "source-is", Tag: "source-tag"},
				UseBuildCache:           true,
			}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BuildRootImage: &BuildRootImageConfiguration{FromRepository: true}}},
			defaultTests: true,
		},
		{
			name:   "base_images is an union of both configs",
			base:   &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BaseImages: baseBaseImages}},
			source: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BaseImages: sourceBaseImages}},
			expected: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BaseImages: map[string]ImageStreamTagReference{
						"base-image":   {Namespace: "base-namespace", Name: "base-image", Tag: "base-tag"},
						"source-image": {Namespace: "source-namespace", Name: "source-image", Tag: "source-tag"},
					}},
			},
			defaultTests: true,
		},
		{
			name: "base_images do not conflict when both configs have a same base image",
			base: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BaseImages: baseBaseImages}},
			source: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{BaseImages: baseBaseImages},
				Tests:              []TestStepConfiguration{sourceTest},
			},
			expected: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BaseImages: map[string]ImageStreamTagReference{
						"base-image": {Namespace: "base-namespace", Name: "base-image", Tag: "base-tag"},
					}},
			},
			defaultTests: true,
		},
		{
			name: "errors when base_images conflict",
			base: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BaseImages: baseBaseImages}},
			source: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{BaseImages: map[string]ImageStreamTagReference{"base-image": {
					Namespace: "another-namespace",
					Name:      "base-image",
					Tag:       "base-tag",
				}},
				},
				Tests: []TestStepConfiguration{sourceTest},
			},
			expectedError: errors.New("conflicting base_images: base-image"),
		},
		{
			name:   "release from source is present in result",
			base:   &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"base-release": {Release: &Release{Version: "4.9.base"}}}}},
			source: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"source-release": {Release: &Release{Version: "4.9.source"}}}}},
			expected: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{
					"base-release":   {Release: &Release{Version: "4.9.base"}},
					"source-release": {Release: &Release{Version: "4.9.source"}}},
				},
			},
			defaultTests: true,
		},
		{
			name:         "release from source is overwrites the one with same name from base",
			base:         &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"source-release": {Release: &Release{Version: "4.9.base"}}}}},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"source-release": {Release: &Release{Version: "4.9.source"}}}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"source-release": {Release: &Release{Version: "4.9.source"}}}}},
			defaultTests: true,
		},
		{
			name:         "latest release from source is added if base does not have tag_specification",
			base:         &ReleaseBuildConfiguration{},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"latest": {Release: &Release{Version: "4.9.source"}}}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"latest": {Release: &Release{Version: "4.9.source"}}}}},
			defaultTests: true,
		},
		{
			name:         "latest release from source is not added if base has tag_specification",
			base:         &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &baseReleaseTagConfiguration}},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"latest": {Release: &Release{Version: "4.9.source"}}}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &baseReleaseTagConfiguration}},
			defaultTests: true,
		},
		{
			name:         "latest release from source is not added if base has releases.latest",
			base:         &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{LatestReleaseName: {Release: &Release{Version: "4.9.base"}}}}},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"latest": {Release: &Release{Version: "4.9.source"}}}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{LatestReleaseName: {Release: &Release{Version: "4.9.base"}}}}},
			defaultTests: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.test == "" {
				tc.test = "source-test"
			}
			if tc.defaultTests {
				tc.source.Tests = []TestStepConfiguration{sourceTest}
				tc.expected.Tests = []TestStepConfiguration{sourceTest}
			}

			actual, err := tc.base.WithPresubmitFrom(tc.source, tc.test)

			if errDiff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); errDiff != "" {
				t.Errorf("Error differs from expected:\n%s", errDiff)
			}

			if diff := cmp.Diff(tc.expected, actual, cmpopts.IgnoreUnexported(ProjectDirectoryImageBuildStepConfiguration{})); tc.expectedError == nil && diff != "" {
				t.Errorf("Result differs from expected:\n%s", diff)
			}
		})
	}
}
