package api

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestPromotesOfficialImages(t *testing.T) {
	var testCases = []struct {
		name       string
		configSpec *ReleaseBuildConfiguration
		expected   bool
	}{
		{
			name: "config without promotion doesn't produce official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: nil,
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to ocp namespace produces official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Targets: []PromotionTarget{{
						Namespace: "ocp",
					}},
				},
			},
			expected: true,
		},
		{
			name: "config with disabled explicit promotion to ocp namespace does not produce official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Targets: []PromotionTarget{{
						Namespace: "ocp",
						Disabled:  true,
					}},
				},
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to okd namespace produces official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Targets: []PromotionTarget{{
						Namespace: "origin",
					}},
				},
			},
			expected: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := PromotesOfficialImages(testCase.configSpec, WithOKD), testCase.expected; actual != expected {
				t.Errorf("%s: did not identify official promotion correctly, expected %v got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestTargetName(t *testing.T) {
	var testCases = []struct {
		name     string
		registry string
		config   PromotionTarget
		expected string
	}{
		{
			name: "with name",
			config: PromotionTarget{
				Namespace: "ns",
				Name:      "name",
			},
			registry: "registry.ci.openshift.org",
			expected: "registry.ci.openshift.org/ns/name:${component}",
		},
		{
			name: "with tag",
			config: PromotionTarget{
				Namespace: "ns",
				Tag:       "tag",
			},
			registry: "registry.ci.openshift.org",
			expected: "registry.ci.openshift.org/ns/${component}:tag",
		},
		{
			name: "quay.io with name",
			config: PromotionTarget{
				Namespace: "ns",
				Name:      "name",
			},
			registry: "quay.io/openshift/ci",
			expected: "quay.io/openshift/ci:ns_name_${component}",
		},
		{
			name: "quay.io with tag",
			config: PromotionTarget{
				Namespace: "ns",
				Tag:       "tag",
			},
			registry: "quay.io/openshift/ci",
			expected: "quay.io/openshift/ci:ns_${component}_tag",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := DefaultTargetNameFunc(testCase.registry, testCase.config)
			if testCase.registry == "quay.io/openshift/ci" {
				actual = QuayTargetNameFunc(testCase.registry, testCase.config)
			}
			if diff := cmp.Diff(testCase.expected, actual); diff != "" {
				t.Errorf("%s: mismatch (-expected +actual), diff: %s", testCase.name, diff)
			}
		})
	}
}

func TestPromotionTargets(t *testing.T) {
	var testCases = []struct {
		name   string
		input  *PromotionConfiguration
		output []PromotionTarget
	}{
		{
			name: "no config",
		},
		{
			name: "only modern config",
			input: &PromotionConfiguration{
				Targets: []PromotionTarget{{
					Namespace:        "ns",
					Name:             "name",
					Tag:              "tag",
					TagByCommit:      true,
					ExcludedImages:   []string{"*"},
					AdditionalImages: map[string]string{"whatever": "else"},
					Disabled:         true,
				}, {
					Namespace:        "new-ns",
					Name:             "new-name",
					Tag:              "new-tag",
					TagByCommit:      true,
					ExcludedImages:   []string{"new-*"},
					AdditionalImages: map[string]string{"new-whatever": "new-else"},
					Disabled:         true,
				}},
			},
			output: []PromotionTarget{{
				Namespace:        "ns",
				Name:             "name",
				Tag:              "tag",
				TagByCommit:      true,
				ExcludedImages:   []string{"*"},
				AdditionalImages: map[string]string{"whatever": "else"},
				Disabled:         true,
			}, {
				Namespace:        "new-ns",
				Name:             "new-name",
				Tag:              "new-tag",
				TagByCommit:      true,
				ExcludedImages:   []string{"new-*"},
				AdditionalImages: map[string]string{"new-whatever": "new-else"},
				Disabled:         true,
			}},
		},
	}
	for _, testCase := range testCases {
		if diff := cmp.Diff(PromotionTargets(testCase.input), testCase.output); diff != "" {
			t.Errorf("%s: incorrect targets: %v", testCase.name, diff)
		}
	}
}

func TestQuayCombinedMirrorFunc(t *testing.T) {
	testCases := []struct {
		name     string
		source   string
		target   string
		tag      ImageStreamTagReference
		time     string
		expected map[string]string
	}{
		{
			name:   "name-based promotion with component",
			source: "registry.build02.ci.openshift.org/ci-op-abc/pipeline@sha256:abc123",
			target: "quay.io/openshift/ci:ocp_4.22_ovn-kubernetes",
			tag: ImageStreamTagReference{
				Namespace: "ocp",
				Name:      "4.22",
				Tag:       "ovn-kubernetes",
			},
			time: "20241024102030",
			expected: map[string]string{
				"quay.io/openshift/ci:ocp_4.22_ovn-kubernetes":                      "registry.build02.ci.openshift.org/ci-op-abc/pipeline@sha256:abc123",
				"quay.io/openshift/ci:20241024102030_prune_ocp_4.22_ovn-kubernetes": "quay.io/openshift/ci:ocp_4.22_ovn-kubernetes",
				"registry.ci.openshift.org/ocp/4.22-quay:ovn-kubernetes":            "quay-proxy.ci.openshift.org/openshift/ci:ocp_4.22_ovn-kubernetes",
			},
		},
		{
			name:   "tag-based promotion (fallback)",
			source: "registry.build02.ci.openshift.org/ci-op-abc/pipeline@sha256:def456",
			target: "quay.io/openshift/ci:ocp_ovn-kubernetes_latest",
			tag: ImageStreamTagReference{
				Namespace: "ocp",
				Name:      "",
				Tag:       "ovn-kubernetes",
			},
			time: "20241024103000",
			expected: map[string]string{
				"quay.io/openshift/ci:ocp__ovn-kubernetes":                         "registry.build02.ci.openshift.org/ci-op-abc/pipeline@sha256:def456",
				"quay.io/openshift/ci:20241024103000_prune_ocp__ovn-kubernetes":    "quay.io/openshift/ci:ocp__ovn-kubernetes",
				"registry.ci.openshift.org/ocp/ovn-kubernetes-quay:ovn-kubernetes": "quay-proxy.ci.openshift.org/openshift/ci:ocp__ovn-kubernetes",
			},
		},
		{
			name:   "empty time skips quay mirror",
			source: "registry.build02.ci.openshift.org/ci-op-abc/pipeline@sha256:xyz789",
			target: "quay.io/openshift/ci:ocp_4.22_installer",
			tag: ImageStreamTagReference{
				Namespace: "ocp",
				Name:      "4.22",
				Tag:       "installer",
			},
			time: "",
			expected: map[string]string{
				"registry.ci.openshift.org/ocp/4.22-quay:installer": "quay-proxy.ci.openshift.org/openshift/ci:ocp_4.22_installer",
			},
		},
		{
			name:   "multiple components in same version",
			source: "registry.build02.ci.openshift.org/ci-op-abc/pipeline@sha256:111222",
			target: "quay.io/openshift/ci:ocp_4.20_cluster-api",
			tag: ImageStreamTagReference{
				Namespace: "ocp",
				Name:      "4.20",
				Tag:       "cluster-api",
			},
			time: "20241024104500",
			expected: map[string]string{
				"quay.io/openshift/ci:ocp_4.20_cluster-api":                      "registry.build02.ci.openshift.org/ci-op-abc/pipeline@sha256:111222",
				"quay.io/openshift/ci:20241024104500_prune_ocp_4.20_cluster-api": "quay.io/openshift/ci:ocp_4.20_cluster-api",
				"registry.ci.openshift.org/ocp/4.20-quay:cluster-api":            "quay-proxy.ci.openshift.org/openshift/ci:ocp_4.20_cluster-api",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mirror := make(map[string]string)
			QuayCombinedMirrorFunc(tc.source, tc.target, tc.tag, tc.time, mirror)

			if diff := cmp.Diff(tc.expected, mirror); diff != "" {
				t.Errorf("QuayCombinedMirrorFunc() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
