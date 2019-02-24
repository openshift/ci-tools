package main

import (
	"reflect"
	"testing"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
	"github.com/openshift/ci-operator/pkg/api"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestGenerateBranchedConfigs(t *testing.T) {
	var testCases = []struct {
		name           string
		currentRelease string
		futureRelease  string
		input          configInfo
		output         []configInfo
	}{
		{
			name:           "config that doesn't promote anywhere is ignored",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: configInfo{
				configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: nil,
				},
				repoInfo: config.FilePathElements{
					Org: "org", Repo: "repo", Branch: "branch", Filename: "org-repo-branch.yaml",
				},
			},
			output: nil,
		},
		{
			name:           "config that doesn't promote to official streams is ignored",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: configInfo{
				configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "custom",
						Namespace: "custom",
					},
				},
				repoInfo: config.FilePathElements{
					Org: "org", Repo: "repo", Branch: "branch", Filename: "org-repo-branch.yaml",
				},
			},
			output: nil,
		},
		{
			name:           "config that doesn't promote to release payload is ignored",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: configInfo{
				configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "4.123",
						Namespace: "ocp",
					},
				},
				repoInfo: config.FilePathElements{
					Org: "org", Repo: "repo", Branch: "branch", Filename: "org-repo-branch.yaml",
				},
			},
			output: nil,
		},
		{
			name:           "config that promotes to the current release from master gets a branched config for the current release",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: configInfo{
				configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:             "current-release",
						Namespace:        "ocp",
						ExcludedImages:   []string{},
						AdditionalImages: map[string]string{},
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:         "current-release",
							Namespace:    "ocp",
							TagOverrides: map[string]string{},
						},
					},
				},
				repoInfo: config.FilePathElements{
					Org: "org", Repo: "repo", Branch: "master", Filename: "org-repo-master.yaml",
				},
			},
			output: []configInfo{
				{
					configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:             "current-release",
							Namespace:        "ocp",
							ExcludedImages:   []string{},
							AdditionalImages: map[string]string{},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:         "current-release",
								Namespace:    "ocp",
								TagOverrides: map[string]string{},
							},
						},
					},
					repoInfo: config.FilePathElements{
						Org: "org", Repo: "repo", Branch: "release-current-release", Filename: "org-repo-release-current-release.yaml",
					},
				},
				{
					configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:             "future-release",
							Namespace:        "ocp",
							ExcludedImages:   []string{},
							AdditionalImages: map[string]string{},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:         "future-release",
								Namespace:    "ocp",
								TagOverrides: map[string]string{},
							},
						},
					},
					repoInfo: config.FilePathElements{
						Org: "org", Repo: "repo", Branch: "master", Filename: "org-repo-master.yaml",
					},
				},
			},
		},
		{
			name:           "config that promotes to the current release from an openshift branch gets a branched config for the new release",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: configInfo{
				configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:             "current-release",
						Namespace:        "ocp",
						ExcludedImages:   []string{},
						AdditionalImages: map[string]string{},
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:         "current-release",
							Namespace:    "ocp",
							TagOverrides: map[string]string{},
						},
					},
				},
				repoInfo: config.FilePathElements{
					Org: "org", Repo: "repo", Branch: "openshift-current-release", Filename: "org-repo-openshift-current-release.yaml",
				},
			},
			output: []configInfo{
				{
					configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:             "current-release",
							Namespace:        "ocp",
							ExcludedImages:   []string{},
							AdditionalImages: map[string]string{},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:         "current-release",
								Namespace:    "ocp",
								TagOverrides: map[string]string{},
							},
						},
					},
					repoInfo: config.FilePathElements{
						Org: "org", Repo: "repo", Branch: "openshift-current-release", Filename: "org-repo-openshift-current-release.yaml",
					},
				},
				{
					configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:             "future-release",
							Namespace:        "ocp",
							ExcludedImages:   []string{},
							AdditionalImages: map[string]string{},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:         "future-release",
								Namespace:    "ocp",
								TagOverrides: map[string]string{},
							},
						},
					},
					repoInfo: config.FilePathElements{
						Org: "org", Repo: "repo", Branch: "openshift-future-release", Filename: "org-repo-openshift-future-release.yaml",
					},
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, expected := generateBranchedConfigs(testCase.currentRelease, testCase.futureRelease, testCase.input), testCase.output
			if len(actual) != len(expected) {
				t.Fatalf("%s: did not generate correct amount of output configs, needed %d got %d", testCase.name, len(expected), len(actual))
			}
			for i := range expected {
				if !reflect.DeepEqual(actual[i].repoInfo, expected[i].repoInfo) {
					t.Errorf("%s: got incorrect path elements: %v", testCase.name, diff.ObjectReflectDiff(actual[i].repoInfo, expected[i].repoInfo))
				}
				if !reflect.DeepEqual(actual[i].configuration.PromotionConfiguration, expected[i].configuration.PromotionConfiguration) {
					t.Errorf("%s: got incorrect promotion config: %v", testCase.name, diff.ObjectReflectDiff(actual[i].configuration.PromotionConfiguration, expected[i].configuration.PromotionConfiguration))
				}
				if !reflect.DeepEqual(actual[i].configuration.ReleaseTagConfiguration, expected[i].configuration.ReleaseTagConfiguration) {
					t.Errorf("%s: got incorrect release input config: %v", testCase.name, diff.ObjectReflectDiff(actual[i].configuration.ReleaseTagConfiguration, expected[i].configuration.ReleaseTagConfiguration))
				}
			}
		})
	}
}
