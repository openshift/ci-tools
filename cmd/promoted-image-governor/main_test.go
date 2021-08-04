package main

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestTagsToDelete(t *testing.T) {
	ocp48Stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "4.8",
			Namespace: "ocp",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "some-component"},
				{Tag: "machine-os-content"},
				{Tag: "bar"},
			},
		},
	}

	origin48Stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "4.8",
			Namespace: "origin",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "some-component"},
				{Tag: "bar"},
				{Tag: "machine-os-content"},
				{Tag: "not-mirror-from-ocp"},
			},
		},
	}

	ciSomeStream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-tool",
			Namespace: "ci",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "latest"},
				{Tag: "test"},
			},
		},
	}

	testCases := []struct {
		name            string
		client          ctrlruntimeclient.Client
		promotedTags    []api.ImageStreamTagReference
		toIgnore        []*regexp.Regexp
		imageStreamRefs []ImageStreamRef
		expected        map[api.ImageStreamTagReference]interface{}
		expectedError   error
	}{
		{
			name:   "basic case",
			client: fakeclient.NewFakeClient(ocp48Stream.DeepCopy(), ciSomeStream.DeepCopy(), origin48Stream.DeepCopy()),
			promotedTags: []api.ImageStreamTagReference{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "latest",
				},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "some-component",
				},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "not-mirror-from-ocp",
				},
			},
			toIgnore: []*regexp.Regexp{
				regexp.MustCompile(`^ocp/\S+:machine-os-content$`),
			},
			imageStreamRefs: []ImageStreamRef{
				{
					Namespace:   "origin",
					Name:        "4.8",
					ExcludeTags: []string{"machine-os-content"},
				},
			},
			expected: map[api.ImageStreamTagReference]interface{}{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "test",
				}: nil,
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "bar",
				}: nil,
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "machine-os-content",
				}: nil,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualError := tagsToDelete(context.TODO(), tc.client, tc.promotedTags, tc.toIgnore, tc.imageStreamRefs)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func TestGenerateMappings(t *testing.T) {
	testCases := []struct {
		name            string
		promotedTags    []api.ImageStreamTagReference
		mappingConfig   *OpenshiftMappingConfig
		imageStreamRefs []ImageStreamRef
		expected        map[string]map[string][]string
	}{
		{
			name: "basic case",
			promotedTags: []api.ImageStreamTagReference{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "latest",
				},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "bar",
				},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "foo",
				},
				{
					Namespace: "origin",
					Name:      "4.9",
					Tag:       "bar",
				},
				{
					Namespace: "origin",
					Name:      "4.9",
					Tag:       "some",
				},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "ocp-some",
				},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "ironic-ipa-downloader",
				},
			},
			mappingConfig: &OpenshiftMappingConfig{
				SourceNamespace: "origin",
				TargetNamespace: "openshift",
				SourceRegistry:  "registry.ci.openshift.org",
				TargetRegistry:  "quay.io",
				Images: map[string][]string{
					"4.8": {"4.8", "4.8.0"},
					"4.9": {"4.9", "4.9.0", "latest"},
				},
			},
			imageStreamRefs: []ImageStreamRef{
				{
					Namespace:   "origin",
					Name:        "4.8",
					ExcludeTags: []string{"ironic-ipa-downloader"},
				},
			},
			expected: map[string]map[string][]string{
				"mapping_origin_4_8": {
					"registry.ci.openshift.org/origin/4.8:bar": {"quay.io/openshift/origin-bar:4.8", "quay.io/openshift/origin-bar:4.8.0"},
					"registry.ci.openshift.org/origin/4.8:foo": {"quay.io/openshift/origin-foo:4.8", "quay.io/openshift/origin-foo:4.8.0"},
					"registry.ci.openshift.org/origin/4.8:ocp-some": {
						"quay.io/openshift/origin-ocp-some:4.8",
						"quay.io/openshift/origin-ocp-some:4.8.0",
					},
				},
				"mapping_origin_4_9": {
					"registry.ci.openshift.org/origin/4.9:bar": {
						"quay.io/openshift/origin-bar:4.9",
						"quay.io/openshift/origin-bar:4.9.0",
						"quay.io/openshift/origin-bar:latest",
					},
					"registry.ci.openshift.org/origin/4.9:some": {
						"quay.io/openshift/origin-some:4.9",
						"quay.io/openshift/origin-some:4.9.0",
						"quay.io/openshift/origin-some:latest",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := generateMappings(tc.promotedTags, tc.mappingConfig, tc.imageStreamRefs)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
