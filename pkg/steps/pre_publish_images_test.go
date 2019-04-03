package steps

import (
	"testing"

	imagev1 "github.com/openshift/api/image/v1"
	fakeimageclientset "github.com/openshift/client-go/image/clientset/versioned/fake"

	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-operator/pkg/api"
)

func TestPrePublishOutputImageTagStep(t *testing.T) {
	config := api.PrePublishOutputImageTagStepConfiguration{
		From: api.PipelineImageStreamTagReference("some-image"),
		To: api.PrePublishImageTagConfiguration{
			Namespace: "configToNamespace",
			Name:      "configToName",
		},
	}

	fakecs := ciopTestingClient{
		kubecs:  nil,
		imagecs: fakeimageclientset.NewSimpleClientset(),
		t:       t,
	}

	client := fakecs.imagecs.ImageV1()

	is := &imagev1.ImageStream{
		ObjectMeta: meta.ObjectMeta{Name: config.To.Name},
		Status:     imagev1.ImageStreamStatus{PublicDockerImageRepository: "uri://somewhere"},
	}

	if _, err := client.ImageStreams(config.To.Namespace).Create(is); err != nil {
		t.Errorf("Could not set up testing ImageStream: %v", err)
	}

	jobspec := &api.JobSpec{
		Namespace: "job-namespace",
		Refs: &api.Refs{
			Pulls: []api.Pull{
				{
					Number: 1234,
				},
			},
		},
	}

	ist := &imagev1.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{Name: "pipeline:some-image"},
		Image:      imagev1.Image{ObjectMeta: meta.ObjectMeta{Name: "fromImageName"}},
	}

	if _, err := client.ImageStreamTags(jobspec.Namespace).Create(ist); err != nil {
		t.Errorf("Could not set up testing ImageStreamTag: %v", err)
	}

	prePublishStep := PrePublishOutputImageTagStep(config, client, client, jobspec)

	specification := stepExpectation{
		name: "[prepublish:configToNamespace:configToName]",
		requires: []api.StepLink{
			api.InternalImageLink(config.From),
			api.ReleaseImagesLink(),
		},
		creates: []api.StepLink{
			api.ExternalImageLink(api.ImageStreamTagReference{
				Name:      "configToName",
				Tag:       "pr-1234",
				Namespace: "configToNamespace",
			}),
		},
		provides: providesExpectation{},
		inputs:   inputsExpectation{values: nil, err: false},
	}

	execSpecification := executionExpectation{
		prerun:   doneExpectation{value: false, err: false},
		runError: false,
		postrun:  doneExpectation{value: true, err: false},
	}

	examineStep(t, prePublishStep, specification)
	executeStep(t, prePublishStep, execSpecification, nil)

	expectedImageStreamTag := &imagev1.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      "configToName:pr-1234",
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

	targetImageStreamTag, err := client.ImageStreamTags("configToNamespace").Get("configToName:pr-1234", meta.GetOptions{})
	if err != nil {
		t.Errorf("Failed to get ImageStreamTag 'configToName:configToTag' after step execution: %v", err)
	}

	if !equality.Semantic.DeepEqual(expectedImageStreamTag, targetImageStreamTag) {
		t.Errorf("Different ImageStreamTag 'pipeline:TO' after step execution:\n%s", diff.ObjectReflectDiff(expectedImageStreamTag, targetImageStreamTag))
	}
}
