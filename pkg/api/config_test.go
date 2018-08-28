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
						As:   "unit",
						From: "ignored",
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
						As:   "images",
						From: "ignored",
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "test with `from`",
			config: ReleaseBuildConfiguration{
				InputConfiguration: dummyInput,
				Tests: []TestStepConfiguration{
					{
						As:   "test",
						From: "from",
					},
				},
			},
			expectedValid: true,
		},
		{
			id: "No test type",
			config: ReleaseBuildConfiguration{
				InputConfiguration: dummyInput,
				Tests: []TestStepConfiguration{
					{
						As: "test",
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "Multiple test types",
			config: ReleaseBuildConfiguration{
				InputConfiguration: dummyInput,
				Tests: []TestStepConfiguration{
					{
						As: "test",
						ContainerTestConfiguration: &ContainerTestConfiguration{},
						OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{
							ClusterTestConfiguration{TargetCloud: TargetCloudGCP}},
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "From + ContainerTestConfiguration",
			config: ReleaseBuildConfiguration{
				InputConfiguration: dummyInput,
				Tests: []TestStepConfiguration{
					{
						As:   "test",
						From: "from",
						ContainerTestConfiguration: &ContainerTestConfiguration{},
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "container test without `from`",
			config: ReleaseBuildConfiguration{
				InputConfiguration: dummyInput,
				Tests: []TestStepConfiguration{
					{
						As: "test",
						ContainerTestConfiguration: &ContainerTestConfiguration{},
					},
				},
			},
			expectedValid: false,
		},
		{
			id: "invalid target cloud",
			config: ReleaseBuildConfiguration{
				InputConfiguration: dummyInput,
				Tests: []TestStepConfiguration{
					{
						As: "test",
						OpenshiftAnsibleClusterTestConfiguration: &OpenshiftAnsibleClusterTestConfiguration{},
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
