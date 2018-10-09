package api

import (
	"fmt"
	"testing"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
)

func TestValidateTests(t *testing.T) {
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
					As:       "unit",
					From:     "ignored",
					Commands: "commands",
				},
			},
			expectedValid: true,
		},
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "images"}}`,
			tests: []TestStepConfiguration{
				{
					As:       "images",
					From:     "ignored",
					Commands: "commands",
				},
			},
			expectedValid: false,
		},
		{
			id: "test with `from`",
			tests: []TestStepConfiguration{
				{
					As:       "test",
					From:     "from",
					Commands: "commands",
				},
			},
			expectedValid: true,
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
			id: "From + ContainerTestConfiguration",
			tests: []TestStepConfiguration{
				{
					As:       "test",
					Commands: "commands",
					From:     "from",
					ContainerTestConfiguration: &ContainerTestConfiguration{},
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
					As:   "test",
					From: "from",
				},
			},
			expectedValid: false,
		},
		{
			id: "test with duplicated `as`",
			tests: []TestStepConfiguration{
				{
					As:       "test",
					From:     "from",
					Commands: "commands",
				},
				{
					As:       "test",
					From:     "from",
					Commands: "commands",
				},
			},
			expectedValid: false,
		},
		{
			id: "test without `as`",
			tests: []TestStepConfiguration{
				{
					Commands: "test",
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid cluster profile",
			tests: []TestStepConfiguration{
				{
					As: "test",
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
			release: &ReleaseTagConfiguration{},
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
	}

	for _, tc := range testTestsCases {
		if err := validateTestStepConfiguration("tests", tc.tests, tc.release); err != nil && tc.expectedValid {
			validationErrors = append(validationErrors, parseError(tc.id, err))
		} else if !tc.expectedValid && err == nil {
			validationErrors = append(validationErrors, parseValidError(tc.id))
		}
	}

	if validationErrors != nil {
		t.Errorf("Errors: %v", kerrors.NewAggregate(validationErrors))
	}
}

func TestValidateBuildRoot(t *testing.T) {
	var validationErrors []error
	var testBuildRootCases = []struct {
		id                   string
		buildRootImageConfig BuildRootImageConfiguration
		expectedValid        bool
	}{
		{
			id: "both project_image and image_stream_tag in build_root defined causes error",
			buildRootImageConfig: BuildRootImageConfiguration{
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
			buildRootImageConfig: BuildRootImageConfiguration{},
			expectedValid:        false,
		},
	}

	for _, tc := range testBuildRootCases {
		if err := validateBuildRootImageConfiguration("build_root", &tc.buildRootImageConfig); err != nil && tc.expectedValid {
			validationErrors = append(validationErrors, parseError(tc.id, err))
		} else if !tc.expectedValid && err == nil {
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
		if err := validateImageStreamTagReferenceMap("base_images", tc.baseImages); err != nil && tc.expectedValid {
			validationErrors = append(validationErrors, parseError(tc.id, err))
		} else if !tc.expectedValid && err == nil {
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
		if err := validateImageStreamTagReferenceMap("base_rpm_images", tc.baseRpmImages); err != nil && tc.expectedValid {
			validationErrors = append(validationErrors, parseError(tc.id, err))
		} else if !tc.expectedValid && err == nil {
			validationErrors = append(validationErrors, parseValidError(tc.id))
		}
	}
	if validationErrors != nil {
		t.Errorf("Errors: %v", kerrors.NewAggregate(validationErrors))
	}
}

func parseError(id string, err error) error {
	return fmt.Errorf("%q expected to be valid, got 'Error(%v)' instead", id, err)
}

func parseValidError(id string) error {
	return fmt.Errorf("%q expected to be invalid, but returned valid", id)
}
