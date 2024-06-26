package steps

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
)

func TestInputImageTagStep(t *testing.T) {
	// Prepare a source ImageStreamTag in a mock cluster
	baseImage := api.ImageStreamTagReference{
		Name:      "BASE",
		Namespace: "source-namespace",
		Tag:       "BASETAG",
	}

	config := api.InputImageTagStepConfiguration{
		InputImage: api.InputImage{
			To:        "TO",
			BaseImage: baseImage,
		},
	}

	client := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
		&imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "target-namespace",
				Name:      api.PipelineImageStream,
			},
			Spec: imagev1.ImageStreamSpec{
				// pipeline:* will now be directly referenceable
				LookupPolicy: imagev1.ImageLookupPolicy{Local: true},
				Tags: []imagev1.TagReference{
					{Name: "TO"},
				},
			},
			Status: imagev1.ImageStreamStatus{
				PublicDockerImageRepository: "some-reg/target-namespace/pipeline",
				Tags: []imagev1.NamedTagEventList{
					{
						Tag: "TO",
						Items: []imagev1.TagEvent{
							{
								Image: "sha256:47e2f82dbede8ff990e6e240f82d78830e7558f7b30df7bd8c0693992018b1e3",
							},
						},
					},
				},
			},
		},
		&imagev1.ImageStreamTag{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s:%s", baseImage.Name, baseImage.Tag),
				Namespace: baseImage.Namespace,
			},
			Image: imagev1.Image{ObjectMeta: metav1.ObjectMeta{Name: "ddc0de"}},
		}).Build())

	// Make a step instance
	jobspec := &api.JobSpec{}
	jobspec.SetNamespace("target-namespace")
	iits := InputImageTagStep(&config, client, jobspec)

	// Set up expectations for the step methods
	specification := stepExpectation{
		name:     "[input:TO]",
		requires: nil,
		creates:  []api.StepLink{api.InternalImageLink("TO")},
		provides: providesExpectation{
			params: nil,
		},
		inputs: inputsExpectation{
			values: api.InputDefinition{"quay-proxy.ci.openshift.org/openshift/ci:source-namespace_BASE_BASETAG"},
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
	executeStep(t, iits, execSpecification)

	// Test that executing the step resulted in an expected ImageStreamTag
	// created
	expectedImageStreamTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "pipeline:TO",
			Namespace:       jobspec.Namespace(),
			ResourceVersion: "1",
		},
		Tag: &imagev1.TagReference{
			From: &corev1.ObjectReference{
				Kind: "DockerImage",
				Name: "quay-proxy.ci.openshift.org/openshift/ci:source-namespace_BASE_BASETAG",
			},
			ImportPolicy: imagev1.TagImportPolicy{
				ImportMode: imagev1.ImportModePreserveOriginal,
			},
			ReferencePolicy: imagev1.TagReferencePolicy{
				Type: imagev1.LocalTagReferencePolicy,
			},
		},
	}

	targetImageStreamTag := &imagev1.ImageStreamTag{}
	if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: jobspec.Namespace(), Name: "pipeline:TO"}, targetImageStreamTag); err != nil {

		t.Errorf("Failed to get ImageStreamTag 'pipeline:TO' after step execution: %v", err)
	}

	if !equality.Semantic.DeepEqual(expectedImageStreamTag, targetImageStreamTag) {
		t.Errorf("Different ImageStreamTag 'pipeline:TO' after step execution:\n%s", diff.ObjectReflectDiff(expectedImageStreamTag, targetImageStreamTag))
	}
}
