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
		}).Build(), nil)

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
				Type: imagev1.SourceTagReferencePolicy,
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

func TestInputImageTagStepOfficialSpec(t *testing.T) {
	specPullSpec := "quay-proxy.ci.openshift.org/openshift/ci@sha256:deadbeef"
	baseImage := api.ImageStreamTagReference{
		Namespace: "ocp",
		Name:      "4.22",
		Tag:       "hyperkube",
	}
	quayRef := api.QuayImageReference(baseImage)
	config := api.InputImageTagStepConfiguration{
		InputImage: api.InputImage{
			To:        "ocp_4_22_hyperkube",
			BaseImage: baseImage,
		},
	}
	client := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
		&imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "4.22"},
			Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
				Name: "hyperkube",
				From: &corev1.ObjectReference{Kind: "DockerImage", Name: specPullSpec},
			}}},
		},
		&imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Namespace: "target-namespace", Name: api.PipelineImageStream},
			Spec: imagev1.ImageStreamSpec{
				LookupPolicy: imagev1.ImageLookupPolicy{Local: true},
				Tags:         []imagev1.TagReference{{Name: "ocp_4_22_hyperkube"}},
			},
			Status: imagev1.ImageStreamStatus{
				PublicDockerImageRepository: "some-reg/target-namespace/pipeline",
				Tags: []imagev1.NamedTagEventList{{
					Tag:   "ocp_4_22_hyperkube",
					Items: []imagev1.TagEvent{{Image: "sha256:47e2f82dbede8ff990e6e240f82d78830e7558f7b30df7bd8c0693992018b1e3"}},
				}},
			},
		}).Build(), nil)
	jobspec := &api.JobSpec{}
	jobspec.SetNamespace("target-namespace")
	iits := InputImageTagStep(&config, client, jobspec)
	specification := stepExpectation{
		name:     "[input:ocp_4_22_hyperkube]",
		requires: nil,
		creates:  []api.StepLink{api.InternalImageLink("ocp_4_22_hyperkube")},
		inputs:   inputsExpectation{values: api.InputDefinition{quayRef}, err: false},
	}
	examineStep(t, iits, specification)
	executeStep(t, iits, executionExpectation{
		prerun:   doneExpectation{value: false, err: false},
		runError: false,
		postrun:  doneExpectation{value: true, err: false},
	})
	expectedImageStreamTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{Name: "pipeline:ocp_4_22_hyperkube", Namespace: jobspec.Namespace(), ResourceVersion: "1"},
		Tag: &imagev1.TagReference{
			From:            &corev1.ObjectReference{Kind: "DockerImage", Name: specPullSpec},
			ImportPolicy:    imagev1.TagImportPolicy{ImportMode: imagev1.ImportModePreserveOriginal},
			ReferencePolicy: imagev1.TagReferencePolicy{Type: imagev1.LocalTagReferencePolicy},
		},
	}
	targetImageStreamTag := &imagev1.ImageStreamTag{}
	if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: jobspec.Namespace(), Name: "pipeline:ocp_4_22_hyperkube"}, targetImageStreamTag); err != nil {
		t.Fatalf("failed to get pipeline tag: %v", err)
	}
	if !equality.Semantic.DeepEqual(expectedImageStreamTag, targetImageStreamTag) {
		t.Errorf("unexpected pipeline tag:\n%s", diff.ObjectReflectDiff(expectedImageStreamTag, targetImageStreamTag))
	}
}

func TestInputImageTagStepLegacyStream(t *testing.T) {
	baseImage := api.ImageStreamTagReference{Namespace: "ocp", Name: "5.0", Tag: "cli"}
	config := api.InputImageTagStepConfiguration{
		InputImage: api.InputImage{To: "cli", BaseImage: baseImage},
	}
	quayRef := api.QuayImageReference(baseImage)
	client := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
		&imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Namespace: "target-namespace", Name: api.PipelineImageStream},
			Spec: imagev1.ImageStreamSpec{
				LookupPolicy: imagev1.ImageLookupPolicy{Local: true},
				Tags:         []imagev1.TagReference{{Name: "cli"}},
			},
			Status: imagev1.ImageStreamStatus{
				PublicDockerImageRepository: "some-reg/target-namespace/pipeline",
				Tags: []imagev1.NamedTagEventList{{
					Tag:   "cli",
					Items: []imagev1.TagEvent{{Image: "sha256:47e2f82dbede8ff990e6e240f82d78830e7558f7b30df7bd8c0693992018b1e3"}},
				}},
			},
		}).Build(), nil)
	jobspec := &api.JobSpec{}
	jobspec.SetNamespace("target-namespace")
	iits := InputImageTagStep(&config, client, jobspec)
	examineStep(t, iits, stepExpectation{
		name:    "[input:cli]",
		creates: []api.StepLink{api.InternalImageLink("cli")},
		inputs:  inputsExpectation{values: api.InputDefinition{quayRef}, err: false},
	})
	executeStep(t, iits, executionExpectation{
		prerun: doneExpectation{value: false, err: false}, runError: false, postrun: doneExpectation{value: true, err: false},
	})
	expected := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{Name: "pipeline:cli", Namespace: jobspec.Namespace(), ResourceVersion: "1"},
		Tag: &imagev1.TagReference{
			From:            &corev1.ObjectReference{Kind: "DockerImage", Name: quayRef},
			ImportPolicy:    imagev1.TagImportPolicy{ImportMode: imagev1.ImportModePreserveOriginal},
			ReferencePolicy: imagev1.TagReferencePolicy{Type: imagev1.SourceTagReferencePolicy},
		},
	}
	got := &imagev1.ImageStreamTag{}
	if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: jobspec.Namespace(), Name: "pipeline:cli"}, got); err != nil {
		t.Fatalf("get pipeline tag: %v", err)
	}
	if !equality.Semantic.DeepEqual(expected, got) {
		t.Errorf("unexpected tag:\n%s", diff.ObjectReflectDiff(expected, got))
	}
}

func TestInputImageTagStepStableFirst(t *testing.T) {
	baseImage := api.ImageStreamTagReference{Namespace: "ocp", Name: "4.22", Tag: "cli"}
	config := api.InputImageTagStepConfiguration{
		InputImage: api.InputImage{To: "ocp_4_22_cli", BaseImage: baseImage},
	}
	client := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
		&imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Namespace: "target-namespace", Name: api.StableImageStream},
			Spec:       imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{Name: "cli"}}},
			Status: imagev1.ImageStreamStatus{
				PublicDockerImageRepository: "registry/target-namespace/stable",
				Tags: []imagev1.NamedTagEventList{{
					Tag:   "cli",
					Items: []imagev1.TagEvent{{Image: "sha256:1111"}},
				}},
			},
		},
		&imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Namespace: "target-namespace", Name: api.PipelineImageStream},
			Spec: imagev1.ImageStreamSpec{
				LookupPolicy: imagev1.ImageLookupPolicy{Local: true},
				Tags:         []imagev1.TagReference{{Name: "ocp_4_22_cli"}},
			},
			Status: imagev1.ImageStreamStatus{
				PublicDockerImageRepository: "registry/target-namespace/pipeline",
				Tags: []imagev1.NamedTagEventList{{
					Tag:   "ocp_4_22_cli",
					Items: []imagev1.TagEvent{{Image: "sha256:2222"}},
				}},
			},
		}).Build(), nil)
	jobspec := &api.JobSpec{}
	jobspec.SetNamespace("target-namespace")
	iits := InputImageTagStep(&config, client, jobspec)
	executeStep(t, iits, executionExpectation{
		prerun: doneExpectation{value: false, err: false}, runError: false, postrun: doneExpectation{value: true, err: false},
	})
	expected := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{Name: "pipeline:ocp_4_22_cli", Namespace: jobspec.Namespace(), ResourceVersion: "1"},
		Tag: &imagev1.TagReference{
			From: &corev1.ObjectReference{
				Kind: "ImageStreamTag", Name: "stable:cli", Namespace: jobspec.Namespace(),
			},
			ImportPolicy:    imagev1.TagImportPolicy{ImportMode: imagev1.ImportModePreserveOriginal},
			ReferencePolicy: imagev1.TagReferencePolicy{Type: imagev1.LocalTagReferencePolicy},
		},
	}
	got := &imagev1.ImageStreamTag{}
	if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: jobspec.Namespace(), Name: "pipeline:ocp_4_22_cli"}, got); err != nil {
		t.Fatalf("get pipeline tag: %v", err)
	}
	if !equality.Semantic.DeepEqual(expected, got) {
		t.Errorf("unexpected tag:\n%s", diff.ObjectReflectDiff(expected, got))
	}
}

func TestInputImageTagStepExternal(t *testing.T) {
	config := api.InputImageTagStepConfiguration{
		InputImage: api.InputImage{
			To: "TO",
			ExternalImage: &api.ExternalImage{
				Registry: "quay.io/openshift/ci",
				ImageStreamTagReference: api.ImageStreamTagReference{
					Namespace: "component",
					Name:      "foo",
					Tag:       "tag",
				},
			},
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
				PublicDockerImageRepository: "quay.io/openshift/ci/component/foo:tag",
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
			Image: imagev1.Image{ObjectMeta: metav1.ObjectMeta{Name: "ddc0de"}},
		}).Build(), nil)

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
			values: api.InputDefinition{"quay.io/openshift/ci/component/foo:tag"},
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
				Name: "quay.io/openshift/ci/component/foo:tag",
			},
			ImportPolicy: imagev1.TagImportPolicy{
				ImportMode: imagev1.ImportModePreserveOriginal,
			},
			ReferencePolicy: imagev1.TagReferencePolicy{
				Type: imagev1.SourceTagReferencePolicy,
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
