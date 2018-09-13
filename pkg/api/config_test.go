package api

import (
	"testing"
)

func TestValidate(t *testing.T) {
	var testCases = []struct {
		id            string
		config        ReleaseBuildConfiguration
		expectedValid bool
		expectedError string
	}{
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "unit"}}`,
			config: ReleaseBuildConfiguration{
				Tests: []TestStepConfiguration{
					{
						As: "unit",
					},
				},
			},
			expectedValid: true,
		},
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "images"}}`,
			config: ReleaseBuildConfiguration{
				Tests: []TestStepConfiguration{
					{
						As: "images",
					},
				},
			},
			expectedValid: false,
			expectedError: "test should not be called 'images' because it gets confused with '[images]' target",
		},
		{
			id: "both git_source_image and image_stream_tag in build_root defined causes error",
			config: ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BuildRootImage: &BuildRootImageConfiguration{
						ImageStreamTagReference: &ImageStreamTagReference{
							Cluster:   "test_cluster",
							Namespace: "test_namespace",
							Name:      "test_name",
							Tag:       "test",
						},
						ProjectImageBuild: &ProjectDirectoryImageBuildInputs{
							ContextDir:     "/",
							DockerfilePath: "Dockerfile.test",
						},
					},
				},
			},
			expectedValid: false,
			expectedError: "both git_source_image and image_stream_tag cannot be set for the build_root",
		},
		{
			id: "build root without any content causes an error",
			config: ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BuildRootImage: &BuildRootImageConfiguration{},
				},
			},
			expectedValid: false,
			expectedError: "you have to specify either git_source_image or image_stream_tag for the build_root",
		},
		{
			id: "build_root and test_base_image defined causes an error",
			config: ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BuildRootImage: &BuildRootImageConfiguration{},
					TestBaseImage:  &ImageStreamTagReference{},
				},
			},
			expectedValid: false,
			expectedError: "both build_root and test_base_image cannot be set",
		},
		{
			id: "build_root and test_base_image not defined causes an error",
			config: ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{},
			},
			expectedValid: false,
			expectedError: "no build_root or test_base_image has been set",
		},
	}

	for _, tc := range testCases {
		err := tc.config.Validate()
		valid := err == nil

		if tc.expectedValid && !valid {
			t.Errorf("%s expected to be valid, got 'Error(%v)' instead", tc.id, err)
		}
		if !tc.expectedValid {
			if valid {
				t.Errorf("%s expected to be invalid, Validate() returned valid", tc.id)
			} else if tc.expectedError != err.Error() {
				t.Errorf("%s expected to be invalid w/ '%s', got '%s' instead", tc.id, tc.expectedError, err.Error())
			}
		}
	}
}
