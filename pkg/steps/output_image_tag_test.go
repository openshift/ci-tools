package steps

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add imagev1 to scheme: %v", err))
	}
}

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
			api.ReleaseImagesLink(api.LatestReleaseName),
		},
		creates: []api.StepLink{
			api.ExternalImageLink(config.To),
			api.InternalImageLink("configToAs"),
		},
		provides: providesExpectation{
			params: map[string]string{"IMAGE_CONFIGTOAS": "uri://somewhere@fromImageName"},
		},
		inputs: inputsExpectation{values: nil, err: false},
	}

	pipelineRoot := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{Name: "pipeline:root", Namespace: jobspec.Namespace()},
		Image:      imagev1.Image{ObjectMeta: metav1.ObjectMeta{Name: "fromImageName"}},
	}

	outputImageStream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Name: config.To.Name, Namespace: config.To.Namespace},
		Status: imagev1.ImageStreamStatus{
			PublicDockerImageRepository: "uri://somewhere",
			Tags: []imagev1.NamedTagEventList{{
				Tag: "configToTag",
				Items: []imagev1.TagEvent{{
					Image: "fromImageName",
				}},
			}},
		},
	}
	outputImageStreamTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
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
		name  string
		input []runtime.Object

		execSpecification      executionExpectation
		expectedImageStreamTag *imagev1.ImageStreamTag
	}{
		{
			name: "image stream exists and creates new image stream",
			input: []runtime.Object{
				outputImageStream,
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
			input: []runtime.Object{
				outputImageStream,
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

	ctx := context.Background()
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			client := fakectrlruntimeclient.NewFakeClient(tt.input...)

			oits := OutputImageTagStep(config, client, jobspec)

			examineStep(t, oits, stepSpec)
			executeStep(t, oits, tt.execSpecification, nil)

			targetImageStreamTag := &imagev1.ImageStreamTag{}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{
				Namespace: tt.expectedImageStreamTag.Namespace,
				Name:      tt.expectedImageStreamTag.Name}, targetImageStreamTag); err != nil {
				t.Errorf("Failed to get ImageStreamTag '%s/%s' after step execution: %v", tt.expectedImageStreamTag.Namespace, tt.expectedImageStreamTag, err)
			}

			targetImageStreamTag.TypeMeta = metav1.TypeMeta{}
			targetImageStreamTag.ResourceVersion = ""
			if !equality.Semantic.DeepEqual(tt.expectedImageStreamTag, targetImageStreamTag) {
				t.Errorf("Different ImageStreamTag 'pipeline:TO' after step execution:\n%s", diff.ObjectReflectDiff(tt.expectedImageStreamTag, targetImageStreamTag))
			}
		})
	}
}
