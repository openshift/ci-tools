package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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

func TestValidatePromotedTags(t *testing.T) {

	testCases := []struct {
		name     string
		tags     []api.ImageStreamTagReference
		expected error
	}{
		{
			name: "empty set is valid",
		},
		{
			name: "valid case",
			tags: []api.ImageStreamTagReference{
				{
					Name:      "bar",
					Namespace: "namespace",
					Tag:       "tag",
				},
				{
					Name:      "foo",
					Namespace: "namespace",
					Tag:       "tag",
				},
			},
		},
		{
			name: "invalid case",
			tags: []api.ImageStreamTagReference{
				{
					Name:      "bar",
					Namespace: "namespace",
					Tag:       "tag",
				},
				{
					Name:      "bar",
					Namespace: "namespace",
					Tag:       "tag",
				},
			},
			expected: fmt.Errorf("found tags promoted more than once: [namespace/bar:tag]"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := validatePromotedTags(tc.tags)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
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
		toIgnore        []api.ImageStreamTagReference
		imageStreamRefs []ImageStreamRef
		expected        []api.ImageStreamTagReference
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
			toIgnore: []api.ImageStreamTagReference{
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "machine-os-content",
				},
			},
			imageStreamRefs: []ImageStreamRef{
				{
					Namespace:   "origin",
					Name:        "4.8",
					ExcludeTags: []string{"machine-os-content"},
				},
			},
			expected: []api.ImageStreamTagReference{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "test",
				},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "bar",
				},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "machine-os-content",
				},
			},
		},
	}

	for _, tc := range testCases {
		opt := cmpopts.SortSlices(func(a, b interface{}) bool {
			if a.(api.ImageStreamTagReference).Namespace != b.(api.ImageStreamTagReference).Namespace {
				return a.(api.ImageStreamTagReference).Namespace < b.(api.ImageStreamTagReference).Namespace
			}
			if a.(api.ImageStreamTagReference).Name != b.(api.ImageStreamTagReference).Name {
				return a.(api.ImageStreamTagReference).Name < b.(api.ImageStreamTagReference).Name
			}
			return a.(api.ImageStreamTagReference).Tag < b.(api.ImageStreamTagReference).Tag
		})

		t.Run(tc.name, func(t *testing.T) {
			actual, actualError := tagsToDelete(context.TODO(), tc.client, tc.promotedTags, tc.toIgnore, tc.imageStreamRefs)
			if diff := cmp.Diff(tc.expected, actual, opt); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
