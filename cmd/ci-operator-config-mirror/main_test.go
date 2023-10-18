package main

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestPrivateReleaseTagConfiguration(t *testing.T) {
	testCases := []struct {
		id               string
		tagSpecification *api.ReleaseTagConfiguration
		expected         *api.ReleaseTagConfiguration
	}{
		{
			id: "no changes expected",
			tagSpecification: &api.ReleaseTagConfiguration{
				Name:      "origin-v4",
				Namespace: "openshift",
			},
			expected: &api.ReleaseTagConfiguration{
				Name:      "origin-v4",
				Namespace: "openshift",
			},
		},
		{
			id: "changes expected",
			tagSpecification: &api.ReleaseTagConfiguration{
				Name:      "origin-v4",
				Namespace: "ocp",
			},
			expected: &api.ReleaseTagConfiguration{
				Name:      "origin-v4-priv",
				Namespace: "ocp-private",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privateReleaseTagConfiguration(tc.tagSpecification)
			if !reflect.DeepEqual(tc.tagSpecification, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.tagSpecification, tc.expected))
			}
		})
	}
}

func TestPrivateIntegrationRelease(t *testing.T) {
	testCases := []struct {
		id       string
		release  *api.Integration
		expected *api.Integration
	}{
		{
			id: "no changes expected",
			release: &api.Integration{
				Name:      "origin-v4",
				Namespace: "openshift",
			},
			expected: &api.Integration{
				Name:      "origin-v4",
				Namespace: "openshift",
			},
		},
		{
			id: "changes expected",
			release: &api.Integration{
				Name:      "origin-v4",
				Namespace: "ocp",
			},
			expected: &api.Integration{
				Name:      "origin-v4-priv",
				Namespace: "ocp-private",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privateIntegrationRelease(tc.release)
			if !reflect.DeepEqual(tc.release, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.release, tc.expected))
			}
		})
	}
}

func TestPrivateBuildRoot(t *testing.T) {
	testCases := []struct {
		id        string
		buildRoot *api.BuildRootImageConfiguration
		expected  *api.BuildRootImageConfiguration
	}{
		{
			id: "no changes expected",
			buildRoot: &api.BuildRootImageConfiguration{
				ImageStreamTagReference: &api.ImageStreamTagReference{
					Name:      "origin-v4",
					Namespace: "openshift",
				},
			},
			expected: &api.BuildRootImageConfiguration{
				ImageStreamTagReference: &api.ImageStreamTagReference{
					Name:      "origin-v4",
					Namespace: "openshift",
				},
			},
		},
		{
			id: "changes expected",
			buildRoot: &api.BuildRootImageConfiguration{
				ImageStreamTagReference: &api.ImageStreamTagReference{
					Name:      "origin-v4",
					Namespace: "ocp",
				},
			},
			expected: &api.BuildRootImageConfiguration{
				ImageStreamTagReference: &api.ImageStreamTagReference{
					Name:      "origin-v4-priv",
					Namespace: "ocp-private",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privateBuildRoot(tc.buildRoot)
			if !reflect.DeepEqual(tc.buildRoot, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.buildRoot, tc.expected))
			}
		})
	}
}

func TestPrivateBaseImages(t *testing.T) {
	testCases := []struct {
		id         string
		baseImages map[string]api.ImageStreamTagReference
		expected   map[string]api.ImageStreamTagReference
	}{
		{
			id: "no changes",
			baseImages: map[string]api.ImageStreamTagReference{
				"base": {Name: "origin-v4", Namespace: "openshift"},
				"os":   {Name: "centos", Namespace: "openshift"},
			},
			expected: map[string]api.ImageStreamTagReference{
				"base": {Name: "origin-v4", Namespace: "openshift"},
				"os":   {Name: "centos", Namespace: "openshift"},
			},
		},

		{
			id: "partly changes",
			baseImages: map[string]api.ImageStreamTagReference{
				"base": {Name: "origin-v4", Namespace: "openshift"},
				"os":   {Name: "centos", Namespace: "ocp"},
				"test": {Name: "4.3", Namespace: "ocp"},
			},
			expected: map[string]api.ImageStreamTagReference{
				"base": {Name: "origin-v4", Namespace: "openshift"},
				"os":   {Name: "centos", Namespace: "ocp"},
				"test": {Name: "4.3-priv", Namespace: "ocp-private"},
			},
		},

		{
			id: "massive changes",
			baseImages: map[string]api.ImageStreamTagReference{
				"base": {Name: "4.2", Namespace: "ocp"},
				"os":   {Name: "4.3", Namespace: "ocp"},
			},
			expected: map[string]api.ImageStreamTagReference{
				"base": {Name: "4.2-priv", Namespace: "ocp-private"},
				"os":   {Name: "4.3-priv", Namespace: "ocp-private"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privateBaseImages(tc.baseImages)
			if !reflect.DeepEqual(tc.baseImages, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.baseImages, tc.expected))
			}
		})
	}
}

func TestPrivatePromotionConfiguration(t *testing.T) {
	testCases := []struct {
		id        string
		promotion *api.PromotionConfiguration
		expected  *api.PromotionConfiguration
	}{
		{
			id:        "promoted by name",
			promotion: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Name: "4.x", Namespace: "ocp"}}},
			expected:  &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Name: "4.x-priv", Namespace: "ocp-private"}}},
		},
		{
			id:        "promoted by tag",
			promotion: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Tag: "4.x", Namespace: "ocp"}}},
			expected:  &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Tag: "4.x-priv", Namespace: "ocp-private"}}},
		},
		{
			id:        "promoted by tag, includes tag_by_commit",
			promotion: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Tag: "4.x", Namespace: "ocp", TagByCommit: true}}},
			expected:  &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Tag: "4.x-priv", Namespace: "ocp-private"}}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privatePromotionConfiguration(tc.promotion)
			if !reflect.DeepEqual(tc.promotion, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.promotion, tc.expected))
			}
		})
	}
}
