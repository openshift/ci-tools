package main

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-operator/pkg/api"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
)

func TestGenerateBranchedConfigs(t *testing.T) {
	var testCases = []struct {
		name           string
		currentRelease string
		futureRelease  string
		input          config.DataWithInfo
		mirror         bool
		output         []config.DataWithInfo
	}{
		{
			name:           "config that doesn't promote anywhere is ignored",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: nil,
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "branch",
				},
			},
			output: nil,
		},
		{
			name:           "config that doesn't promote to official streams is ignored",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "custom",
						Namespace: "custom",
					},
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "branch",
				},
			},
			output: nil,
		},
		{
			name:           "config that doesn't promote to release payload is ignored",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "4.123",
						Namespace: "ocp",
					},
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "branch",
				},
			},
			output: nil,
		},
		{
			name:           "config that promotes to the current release from master gets a branched config for the current release",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "current-release",
						Namespace: "ocp",
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
					},
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "master",
				},
			},
			output: []config.DataWithInfo{
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "current-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Org: "org", Repo: "repo", Branch: "release-current-release",
					},
				},
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:      "future-release",
							Namespace: "ocp",
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "future-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Org: "org", Repo: "repo", Branch: "master",
					},
				},
			},
		},
		{
			name:           "config that promotes to the current release from master gets a mirrored config for the current release",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "current-release",
						Namespace: "ocp",
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
					},
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "master",
				},
			},
			mirror: true,
			output: []config.DataWithInfo{
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
							Disabled:  true,
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "current-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Org: "org", Repo: "repo", Branch: "release-current-release",
					},
				},
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "current-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Org: "org", Repo: "repo", Branch: "master",
					},
				},
			},
		},
		{
			name:           "config that promotes to the current release from an openshift branch gets a branched config for the new release",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "current-release",
						Namespace: "ocp",
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
					},
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "openshift-current-release",
				},
			},
			output: []config.DataWithInfo{
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "current-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Org: "org", Repo: "repo", Branch: "openshift-current-release",
					},
				},
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:      "future-release",
							Namespace: "ocp",
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "future-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Org: "org", Repo: "repo", Branch: "openshift-future-release",
					},
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, expected := generateBranchedConfigs(testCase.currentRelease, testCase.futureRelease, testCase.input, testCase.mirror), testCase.output
			if len(actual) != len(expected) {
				t.Fatalf("%s: did not generate correct amount of output configs, needed %d got %d", testCase.name, len(expected), len(actual))
			}
			for i := range expected {
				if !reflect.DeepEqual(actual[i].Info, expected[i].Info) {
					t.Errorf("%s: got incorrect path elements: %v", testCase.name, diff.ObjectReflectDiff(actual[i].Info, expected[i].Info))
				}
				if !reflect.DeepEqual(actual[i].Configuration.PromotionConfiguration, expected[i].Configuration.PromotionConfiguration) {
					t.Errorf("%s: got incorrect promotion config: %v", testCase.name, diff.ObjectReflectDiff(actual[i].Configuration.PromotionConfiguration, expected[i].Configuration.PromotionConfiguration))
				}
				if !reflect.DeepEqual(actual[i].Configuration.ReleaseTagConfiguration, expected[i].Configuration.ReleaseTagConfiguration) {
					t.Errorf("%s: got incorrect release input config: %v", testCase.name, diff.ObjectReflectDiff(actual[i].Configuration.ReleaseTagConfiguration, expected[i].Configuration.ReleaseTagConfiguration))
				}
			}
		})
	}
}

func TestGenerateUnmirroredConfigs(t *testing.T) {
	var testCases = []struct {
		name           string
		currentRelease string
		futureRelease  string
		input          config.DataWithInfo
		output         []config.DataWithInfo
	}{
		{
			name:           "config that doesn't promote anywhere is ignored",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: nil,
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "branch",
				},
			},
			output: nil,
		},
		{
			name:           "config that doesn't promote to official streams is ignored",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "custom",
						Namespace: "custom",
					},
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "branch",
				},
			},
			output: nil,
		},
		{
			name:           "config that doesn't promote to release payload is ignored",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "4.123",
						Namespace: "ocp",
					},
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "branch",
				},
			},
			output: nil,
		},
		{
			name:           "config that promotes to the current release from master gets bumped for the current release",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "current-release",
						Namespace: "ocp",
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
					},
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "master",
				},
			},
			output: []config.DataWithInfo{
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:      "future-release",
							Namespace: "ocp",
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "future-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Org: "org", Repo: "repo", Branch: "master",
					},
				},
			},
		},
		{
			name:           "config that has a disabled promotion to the current release gets enabled for the current release",
			currentRelease: "current-release",
			futureRelease:  "future-release",
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Name:      "current-release",
						Namespace: "ocp",
						Disabled:  true,
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
					},
				},
				Info: config.Info{
					Org: "org", Repo: "repo", Branch: "release-current-release",
				},
			},
			output: []config.DataWithInfo{
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
							Disabled:  false,
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "current-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Org: "org", Repo: "repo", Branch: "release-current-release",
					},
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, expected := generateUnmirroredConfigs(testCase.currentRelease, testCase.futureRelease, testCase.input), testCase.output
			if len(actual) != len(expected) {
				t.Fatalf("%s: did not generate correct amount of output configs, needed %d got %d", testCase.name, len(expected), len(actual))
			}
			for i := range expected {
				if !reflect.DeepEqual(actual[i].Info, expected[i].Info) {
					t.Errorf("%s: got incorrect path elements: %v", testCase.name, diff.ObjectReflectDiff(actual[i].Info, expected[i].Info))
				}
				if !reflect.DeepEqual(actual[i].Configuration.PromotionConfiguration, expected[i].Configuration.PromotionConfiguration) {
					t.Errorf("%s: got incorrect promotion config: %v", testCase.name, diff.ObjectReflectDiff(actual[i].Configuration.PromotionConfiguration, expected[i].Configuration.PromotionConfiguration))
				}
				if !reflect.DeepEqual(actual[i].Configuration.ReleaseTagConfiguration, expected[i].Configuration.ReleaseTagConfiguration) {
					t.Errorf("%s: got incorrect release input config: %v", testCase.name, diff.ObjectReflectDiff(actual[i].Configuration.ReleaseTagConfiguration, expected[i].Configuration.ReleaseTagConfiguration))
				}
			}
		})
	}
}
