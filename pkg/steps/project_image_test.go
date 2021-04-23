package steps

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
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

func aBuildArgSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-secret",
			Namespace: "ns",
		},
		Data: map[string][]byte{
			"some-key": []byte("bla"),
		},
	}
}

func TestCreateSecrets(t *testing.T) {
	for _, tc := range []struct {
		name       string
		s          *projectDirectoryImageBuildStep
		expected   error
		verifyFunc func(client ctrlruntimeclient.Client) error
	}{{
		name: "happy path",
		s: &projectDirectoryImageBuildStep{
			secretClient: fake.NewClientBuilder().WithObjects(aBuildArgSecret()).Build(),
			jobSpec:      &api.JobSpec{},
			config: api.ProjectDirectoryImageBuildStepConfiguration{
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					BuildArgs: []api.BuildArg{
						{
							Name:  "a",
							Value: "b",
						},
						{
							Name: "c",
							ValueFrom: &api.SecretKeySelector{
								Namespace: "ns",
								Name:      "some-secret",
								Key:       "some-key",
							},
						},
					},
				},
			},
		},
		verifyFunc: func(client ctrlruntimeclient.Client) error {
			ctx := context.TODO()
			actualSecret := &corev1.Secret{}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "ns-some-secret", Namespace: "ci-op-zcsc2986"}, actualSecret); err != nil {
				return err
			}
			expectedSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ns-some-secret",
					Namespace: "ci-op-zcsc2986",
				},
				Data: map[string][]byte{
					"some-key": []byte("bla"),
				},
			}
			if diff := cmp.Diff(expectedSecret, actualSecret, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
				return fmt.Errorf("actual does not match expected, diff: %s", diff)
			}
			return nil
		},
	}, {
		name: "secret not found",
		s: &projectDirectoryImageBuildStep{
			secretClient: fake.NewClientBuilder().WithObjects().Build(),
			jobSpec:      &api.JobSpec{},
			config: api.ProjectDirectoryImageBuildStepConfiguration{
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					BuildArgs: []api.BuildArg{
						{
							Name: "c",
							ValueFrom: &api.SecretKeySelector{
								Namespace: "ns",
								Name:      "some-secret",
								Key:       "some-key",
							},
						},
					},
				},
			},
		},
		expected: fmt.Errorf("could not read source secret some-secret in namespace ns: %w", kerrors.NewNotFound(corev1.Resource("secrets"), "some-secret")),
		verifyFunc: func(client ctrlruntimeclient.Client) error {
			ctx := context.TODO()
			actualSecret := &corev1.Secret{}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "ns-some-secret", Namespace: "ci-op-zcsc2986"}, actualSecret); !kerrors.IsNotFound(err) {
				return fmt.Errorf("expected Not Found error did not occur")
			}
			return nil
		},
	}, {
		name: "failed to determine the namespaced name",
		s: &projectDirectoryImageBuildStep{
			secretClient: fake.NewClientBuilder().WithObjects().Build(),
			jobSpec:      &api.JobSpec{},
			config: api.ProjectDirectoryImageBuildStepConfiguration{
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					BuildArgs: []api.BuildArg{
						{
							Name: "c",
							ValueFrom: &api.SecretKeySelector{
								Namespace: "ns",
								Name:      "some-secret",
							},
						},
					},
				},
			},
		},
		expected: fmt.Errorf("build_args[%d] failed to determine the namespaced name: %w", 0, fmt.Errorf("key must be set")),
		verifyFunc: func(client ctrlruntimeclient.Client) error {
			ctx := context.TODO()
			actualSecret := &corev1.Secret{}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "ns-some-secret", Namespace: "ci-op-zcsc2986"}, actualSecret); !kerrors.IsNotFound(err) {
				return fmt.Errorf("expected Not Found error did not occur")
			}
			return nil
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			tc.s.jobSpec.SetNamespace("ci-op-zcsc2986")
			actual := tc.s.createSecrets(context.TODO())
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if tc.verifyFunc != nil {
				if err := tc.verifyFunc(tc.s.secretClient); err != nil {
					t.Errorf("%s: an unexpected error occurred: %v", tc.name, err)
				}
			}
		})
	}
}
