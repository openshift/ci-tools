package steps

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestImagesFor(t *testing.T) {
	var testCases = []struct {
		name          string
		config        api.ProjectDirectoryImageBuildStepConfiguration
		workingDir    workingDir
		isBundleImage isBundleImage
		sourceTag     api.PipelineImageStreamTagReference
		images        []buildapi.ImageSource
		expectError   bool
	}{
		{
			name: "normal build",
			config: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "output",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"input": {
							Paths: []api.ImageSourcePath{
								{SourcePath: "first-source", DestinationDir: "first-dest"},
								{SourcePath: "second-source", DestinationDir: "second-dest"},
							},
							As: []string{"asname", "asother"},
						},
					},
					ContextDir: "context",
				},
			},
			workingDir: func(tag string) (string, error) {
				return "dir", nil
			},
			isBundleImage: func(tag string) bool {
				return false
			},
			sourceTag: api.PipelineImageStreamTagReferenceSource,
			images: []buildapi.ImageSource{
				{
					From: corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "pipeline:input",
					},
					As: []string{"asname", "asother"},
					Paths: []buildapi.ImageSourcePath{
						{SourcePath: "first-source", DestinationDir: "first-dest"},
						{SourcePath: "second-source", DestinationDir: "second-dest"},
					},
				},
				{
					From: corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "pipeline:src",
					},
					Paths: []buildapi.ImageSourcePath{
						{SourcePath: "dir/context/.", DestinationDir: "."},
					},
				},
			},
			expectError: false,
		},
		{
			name: "user overwrites input",
			config: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "output",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"input": {
							Paths: []api.ImageSourcePath{
								{SourcePath: "first-source", DestinationDir: "first-dest"},
								{SourcePath: "second-source", DestinationDir: "second-dest"},
							},
							As: []string{"asname", "asother"},
						},
						"src": {
							Paths: []api.ImageSourcePath{
								{SourcePath: "custom-source", DestinationDir: "custom-dest"},
							},
							As: []string{"assrc"},
						},
					},
					ContextDir: "context",
				},
			},
			workingDir: func(tag string) (string, error) {
				return "dir", nil
			},
			isBundleImage: func(tag string) bool {
				return false
			},
			sourceTag: api.PipelineImageStreamTagReferenceSource,
			images: []buildapi.ImageSource{
				{
					From: corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "pipeline:input",
					},
					As: []string{"asname", "asother"},
					Paths: []buildapi.ImageSourcePath{
						{SourcePath: "first-source", DestinationDir: "first-dest"},
						{SourcePath: "second-source", DestinationDir: "second-dest"},
					},
				},
				{
					From: corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "pipeline:src",
					},
					As: []string{"assrc"},
					Paths: []buildapi.ImageSourcePath{
						{SourcePath: "custom-source", DestinationDir: "custom-dest"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "fails to get working dir",
			config: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "output",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"input": {
							Paths: []api.ImageSourcePath{
								{SourcePath: "first-source", DestinationDir: "first-dest"},
								{SourcePath: "second-source", DestinationDir: "second-dest"},
							},
							As: []string{"asname", "asother"},
						},
					},
					ContextDir: "context",
				},
			},
			workingDir: func(tag string) (string, error) {
				return "dir", errors.New("oops")
			},
			isBundleImage: func(tag string) bool {
				return false
			},
			expectError: true,
		},
		{
			name: "bundle image build",
			config: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "output",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"input": {
							Paths: []api.ImageSourcePath{
								{SourcePath: "first-source", DestinationDir: "first-dest"},
								{SourcePath: "second-source", DestinationDir: "second-dest"},
							},
							As: []string{"asname", "asother"},
						},
					},
					ContextDir: "context",
				},
			},
			workingDir: func(tag string) (string, error) {
				return "dir", nil
			},
			isBundleImage: func(tag string) bool {
				return true
			},
			sourceTag: api.PipelineImageStreamTagReferenceBundleSource,
			images: []buildapi.ImageSource{
				{
					From: corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "pipeline:input",
					},
					As: []string{"asname", "asother"},
					Paths: []buildapi.ImageSourcePath{
						{SourcePath: "first-source", DestinationDir: "first-dest"},
						{SourcePath: "second-source", DestinationDir: "second-dest"},
					},
				},
				{
					From: corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "pipeline:src-bundle",
					},
					Paths: []buildapi.ImageSourcePath{
						{SourcePath: "dir/context/.", DestinationDir: "."},
					},
				},
			},
			expectError: false,
		},
		{
			name: "index image build",
			config: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "ci-index-0",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"input": {
							Paths: []api.ImageSourcePath{
								{SourcePath: "first-source", DestinationDir: "first-dest"},
								{SourcePath: "second-source", DestinationDir: "second-dest"},
							},
							As: []string{"asname", "asother"},
						},
					},
					ContextDir: "context",
				},
			},
			workingDir: func(tag string) (string, error) {
				return "dir", nil
			},
			isBundleImage: func(tag string) bool {
				return false
			},
			sourceTag: api.PipelineImageStreamTagReference("ci-index-0-gen"),
			images: []buildapi.ImageSource{
				{
					From: corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "pipeline:input",
					},
					As: []string{"asname", "asother"},
					Paths: []buildapi.ImageSourcePath{
						{SourcePath: "first-source", DestinationDir: "first-dest"},
						{SourcePath: "second-source", DestinationDir: "second-dest"},
					},
				},
				{
					From: corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "pipeline:ci-index-0-gen",
					},
					Paths: []buildapi.ImageSourcePath{
						{SourcePath: "dir/.", DestinationDir: "."},
					},
				},
			},
			expectError: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			sourceTag, images, err := imagesFor(testCase.config, testCase.workingDir, testCase.isBundleImage)
			if testCase.expectError && err == nil {
				t.Errorf("%s: expected an error but got none", testCase.name)
			}
			if !testCase.expectError && err != nil {
				t.Errorf("%s: expected no error but got one: %v", testCase.name, err)
			}
			if diff := cmp.Diff(testCase.sourceTag, sourceTag); diff != "" {
				t.Errorf("%s: got incorrect source tag: %v", testCase.name, diff)
			}
			if diff := cmp.Diff(testCase.images, images); diff != "" {
				t.Errorf("%s: got incorrect images: %v", testCase.name, diff)
			}
		})
	}
}
