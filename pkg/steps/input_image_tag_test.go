package steps

import (
	"fmt"
	"testing"

	apiimagev1 "github.com/openshift/api/image/v1"
	fakeimageclientset "github.com/openshift/client-go/image/clientset/versioned/fake"

	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-operator/pkg/api"
)

func TestInputImageTagStep(t *testing.T) {
	// Prepare a source ImageStreamTag in a mock cluster
	baseImage := api.ImageStreamTagReference{
		Name:      "BASE",
		Namespace: "source-namespace",
		Tag:       "BASETAG",
	}

	config := api.InputImageTagStepConfiguration{
		To:        "TO",
		BaseImage: baseImage,
	}
	istag := &apiimagev1.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", baseImage.Name, baseImage.Tag),
			Namespace: baseImage.Namespace,
		},
		Image: apiimagev1.Image{ObjectMeta: meta.ObjectMeta{Name: "ddc0de"}},
	}

	fakecs := ciopTestingClient{
		kubecs:  nil,
		imagecs: fakeimageclientset.NewSimpleClientset(),
		t:       t,
	}

	srcClient := fakecs.ImageV1()
	dstClient := srcClient

	if _, err := srcClient.ImageStreamTags(baseImage.Namespace).Create(istag); err != nil {
		t.Errorf("Could not set up testing ImageStreamTag: %v", err)
	}

	// Make a step instance
	jobspec := &api.JobSpec{Namespace: "target-namespace"}
	iits := InputImageTagStep(config, srcClient, dstClient, jobspec)

	// Set up expectations for the step methods
	specification := stepExpectation{
		name:     "[input:TO]",
		requires: nil,
		creates:  []api.StepLink{api.InternalImageLink("TO")},
		provides: providesExpectation{
			params: nil,
			link:   nil,
		},
		inputs: inputsExpectation{
			values: api.InputDefinition{"ddc0de"},
			err:    false,
		},
	}

	execSpecification := executionExpectation{
		prerun: doneExpectation{
			value: false,
			err:   false,
		},
		runError: false,
		postrun: doneExpectation{
			value: true,
			err:   false,
		},
	}

	// Test all step methods
	examineStep(t, iits, specification)
	executeStep(t, iits, execSpecification, nil)

	// Test that executing the step resulted in an expected ImageStreamTag
	// created
	expectedImageStreamTag := &apiimagev1.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      "pipeline:TO",
			Namespace: jobspec.Namespace,
		},
		Tag: &apiimagev1.TagReference{
			From: &corev1.ObjectReference{
				Kind:      "ImageStreamImage",
				Namespace: baseImage.Namespace,
				Name:      "BASE@ddc0de",
			},
			ReferencePolicy: apiimagev1.TagReferencePolicy{
				Type: apiimagev1.LocalTagReferencePolicy,
			},
		},
	}

	targetImageStreamTag, err := dstClient.ImageStreamTags(jobspec.Namespace).Get("pipeline:TO", meta.GetOptions{})
	if !equality.Semantic.DeepEqual(expectedImageStreamTag, targetImageStreamTag) {
		t.Errorf("Different ImageStreamTag 'pipeline:TO' after step execution:\n%s", diff.ObjectReflectDiff(expectedImageStreamTag, targetImageStreamTag))
	}
	if err != nil {
		t.Errorf("Failed to get ImageStreamTag 'pipeline:TO' after step execution: %v", err)
	}
}
