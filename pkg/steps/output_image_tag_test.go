package steps

import (
	"testing"

	imagev1 "github.com/openshift/api/image/v1"
	fakeimageclientset "github.com/openshift/client-go/image/clientset/versioned/fake"

	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestOutputImageStep(t *testing.T) {
	config := api.OutputImageTagStepConfiguration{
		From: api.PipelineImageStreamTagReferenceRoot,
		To: api.ImageStreamTagReference{
			As:        "configToAs",
			Name:      "configToName",
			Namespace: "configToNamespace",
			Tag:       "configToTag",
		},
	}
	jobspec := &api.JobSpec{}
	jobspec.SetNamespace("job-namespace")
	stepSpec := stepExpectation{
		name: "configToAs",
		requires: []api.StepLink{
			api.InternalImageLink(config.From),
		},
		creates: []api.StepLink{
			api.ExternalImageLink(config.To),
			api.InternalImageLink("configToAs"),
		},
		provides: providesExpectation{
			params: map[string]string{"IMAGE_CONFIGTOAS": "uri://somewhere:configToTag"},
			link:   api.ExternalImageLink(config.To),
		},
		inputs: inputsExpectation{values: nil, err: false},
	}

	pipelineRoot := &imagev1.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{Name: "pipeline:root", Namespace: jobspec.Namespace()},
		Image:      imagev1.Image{ObjectMeta: meta.ObjectMeta{Name: "fromImageName"}},
	}

	outputImageStream := &imagev1.ImageStream{
		ObjectMeta: meta.ObjectMeta{Name: config.To.Name, Namespace: config.To.Namespace},
		Status:     imagev1.ImageStreamStatus{PublicDockerImageRepository: "uri://somewhere"},
	}
	outputImageStreamTag := &imagev1.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      "configToName:configToTag",
			Namespace: "configToNamespace",
		},
		Tag: &imagev1.TagReference{
			From: &corev1.ObjectReference{
				Kind:      "ImageStreamImage",
				Namespace: "job-namespace",
				Name:      "pipeline@fromImageName",
			},
			ReferencePolicy: imagev1.TagReferencePolicy{Type: imagev1.LocalTagReferencePolicy},
		},
	}

	tests := []struct {
		name            string
		imageStreams    []*imagev1.ImageStream
		imageStreamTags []*imagev1.ImageStreamTag

		execSpecification      executionExpectation
		expectedImageStreamTag *imagev1.ImageStreamTag
	}{
		{
			name: "image stream exists and creates new image stream",
			imageStreams: []*imagev1.ImageStream{
				outputImageStream,
			},
			imageStreamTags: []*imagev1.ImageStreamTag{
				pipelineRoot,
			},
			expectedImageStreamTag: outputImageStreamTag,
			execSpecification: executionExpectation{
				prerun:   doneExpectation{value: false, err: false},
				runError: false,
				postrun:  doneExpectation{value: true, err: false},
			},
		},
		{
			name: "image stream and desired image stream tag exists",
			imageStreams: []*imagev1.ImageStream{
				outputImageStream,
			},
			imageStreamTags: []*imagev1.ImageStreamTag{
				pipelineRoot,
				outputImageStreamTag,
			},
			expectedImageStreamTag: outputImageStreamTag,
			execSpecification: executionExpectation{
				// done is true prerun because the imageStreamTag is already
				// created in this test case, and it matches the desired output
				prerun:   doneExpectation{value: true, err: false},
				runError: false,
				postrun:  doneExpectation{value: true, err: false},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			fakecs := ciopTestingClient{
				kubecs:  nil,
				imagecs: fakeimageclientset.NewSimpleClientset(),
				t:       t,
			}

			client := fakecs.imagecs.ImageV1()

			for _, is := range tt.imageStreams {
				if _, err := client.ImageStreams(is.Namespace).Create(is); err != nil {
					t.Errorf("Could not set up testing ImageStream: %v", err)
				}
			}

			for _, ist := range tt.imageStreamTags {
				if _, err := client.ImageStreamTags(ist.Namespace).Create(ist); err != nil {
					t.Errorf("Could not set up testing ImageStreamTag: %v", err)
				}
			}

			dryLogger := &DryLogger{}
			oits := OutputImageTagStep(config, client, client, jobspec, dryLogger)

			examineStep(t, oits, stepSpec)
			executeStep(t, oits, tt.execSpecification, nil)

			targetImageStreamTag, err := client.ImageStreamTags(tt.expectedImageStreamTag.Namespace).Get(tt.expectedImageStreamTag.Name, meta.GetOptions{})
			if err != nil {
				t.Errorf("Failed to get ImageStreamTag '%s/%s' after step execution: %v", tt.expectedImageStreamTag.Namespace, tt.expectedImageStreamTag, err)
			}

			if !equality.Semantic.DeepEqual(tt.expectedImageStreamTag, targetImageStreamTag) {
				t.Errorf("Different ImageStreamTag 'pipeline:TO' after step execution:\n%s", diff.ObjectReflectDiff(tt.expectedImageStreamTag, targetImageStreamTag))
			}
		})
	}
}
