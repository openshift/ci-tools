package api

import (
	"testing"
)

func TestValidate(t *testing.T) {
	dummyInput := InputConfiguration{
		BuildRootImage: &BuildRootImageConfiguration{
			ProjectImageBuild: &ProjectDirectoryImageBuildInputs{
				DockerfilePath: "ignored"}}}
	var testCases = []struct {
		id            string
		config        ReleaseBuildConfiguration
		expectedValid bool
	}{
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "unit"}}`,
			config: ReleaseBuildConfiguration{
				InputConfiguration: dummyInput,
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
				InputConfiguration: dummyInput,
				Tests: []TestStepConfiguration{
					{
						As: "images",
					},
				},
			},
			expectedValid: false,
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
		},
		{
			id: "build root without any content causes an error",
			config: ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BuildRootImage: &BuildRootImageConfiguration{},
				},
			},
			expectedValid: false,
		},
	}

	for _, tc := range testCases {
		err := tc.config.Validate()
		if tc.expectedValid && err != nil {
			t.Errorf("%q expected to be valid, got 'Error(%v)' instead", tc.id, err)
		} else if !tc.expectedValid && err == nil {
			t.Errorf("%q expected to be invalid, Validate() returned valid", tc.id)
		}
	}
}
