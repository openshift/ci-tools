package main

import (
	"flag"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/test-infra/prow/flagutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

func TestGenerateBranchedConfigs(t *testing.T) {
	interval := "72h"
	cron := "@weekly"
	var testCases = []struct {
		name           string
		currentRelease string
		bumpRelease    string
		futureReleases []string
		input          config.DataWithInfo
		skipPeriodics  bool
		output         []config.DataWithInfo
	}{
		{
			name:           "config that doesn't promote anywhere is ignored",
			currentRelease: "current-release",
			futureReleases: []string{"current-release"},
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: nil,
				},
				Info: config.Info{
					Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				},
			},
			output: nil,
		},
		{
			name:           "config that doesn't promote to official streams is ignored",
			currentRelease: "current-release",
			futureReleases: []string{"current-release"},
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Targets: []api.PromotionTarget{{
							Name:      "custom",
							Namespace: "custom",
						}},
					},
				},
				Info: config.Info{
					Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				},
			},
			output: nil,
		},
		{
			name:           "config that doesn't promote to release payload is ignored",
			currentRelease: "current-release",
			futureReleases: []string{"current-release"},
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Targets: []api.PromotionTarget{{
							Name:      "4.123",
							Namespace: "ocp",
						}},
					},
				},
				Info: config.Info{
					Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				},
			},
			output: nil,
		},
		{
			name:           "config that promotes to the current release from master gets a branched config for the current release",
			currentRelease: "current-release",
			futureReleases: []string{"current-release"},
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Targets: []api.PromotionTarget{{
							Name:      "current-release",
							Namespace: "ocp",
						}},
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
						BaseImages: map[string]api.ImageStreamTagReference{
							"first": {
								Name:      "current-release",
								Namespace: "ocp",
								Tag:       "first",
							},
						},
						BaseRPMImages: map[string]api.ImageStreamTagReference{
							"second": {
								Name:      "current-release",
								Namespace: "ocp",
								Tag:       "second",
							},
						},
						BuildRootImage: &api.BuildRootImageConfiguration{
							ImageStreamTagReference: &api.ImageStreamTagReference{
								Name:      "current-release",
								Namespace: "ocp",
								Tag:       "third",
							},
						},
					},
				},
				Info: config.Info{
					Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "master"},
				},
			},
			output: []config.DataWithInfo{
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{{
								Name:      "current-release",
								Namespace: "ocp",
								Disabled:  true,
							}},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "current-release",
								Namespace: "ocp",
							},
							BaseImages: map[string]api.ImageStreamTagReference{
								"first": {
									Name:      "current-release",
									Namespace: "ocp",
									Tag:       "first",
								},
							},
							BaseRPMImages: map[string]api.ImageStreamTagReference{
								"second": {
									Name:      "current-release",
									Namespace: "ocp",
									Tag:       "second",
								},
							},
							BuildRootImage: &api.BuildRootImageConfiguration{
								ImageStreamTagReference: &api.ImageStreamTagReference{
									Name:      "current-release",
									Namespace: "ocp",
									Tag:       "third",
								},
							},
						},
					},
					Info: config.Info{
						Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "release-current-release"},
					},
				},
			},
		},
		{
			name:           "config that promotes to the current release from an non-dev branch gets no new config for the current release",
			currentRelease: "current-release",
			futureReleases: []string{"current-release"},
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Targets: []api.PromotionTarget{{
							Name:      "current-release",
							Namespace: "ocp",
						}},
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
					},
				},
				Info: config.Info{
					Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "openshift-current-release"},
				},
			},
			output: []config.DataWithInfo{},
		},
		{
			name:           "config with tests that promotes to the current release from master gets a branched config for the every future release without skipped tests",
			currentRelease: "current-release",
			futureReleases: []string{"current-release", "future-release-1", "future-release-2"},
			skipPeriodics:  true,
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					Tests: []api.TestStepConfiguration{
						{As: "periodic-interval", Interval: &interval},
						{As: "periodic-cron", Cron: &cron},
						{As: "periodic-cron-portable", Cron: &cron, Portable: true},
					},
					PromotionConfiguration: &api.PromotionConfiguration{
						Targets: []api.PromotionTarget{{
							Name:      "current-release",
							Namespace: "ocp",
						}},
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
						BaseImages: map[string]api.ImageStreamTagReference{
							"first": {
								Name:      "current-release",
								Namespace: "ocp",
								Tag:       "first",
							},
						},
						BaseRPMImages: map[string]api.ImageStreamTagReference{
							"second": {
								Name:      "current-release",
								Namespace: "ocp",
								Tag:       "second",
							},
						},
						BuildRootImage: &api.BuildRootImageConfiguration{
							ImageStreamTagReference: &api.ImageStreamTagReference{
								Name:      "current-release",
								Namespace: "ocp",
								Tag:       "third",
							},
						},
					},
				},
				Info: config.Info{
					Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "master"},
				},
			},
			output: []config.DataWithInfo{
				{
					Configuration: api.ReleaseBuildConfiguration{
						Tests: []api.TestStepConfiguration{
							{As: "periodic-cron-portable", Cron: &cron, Portable: true},
						},
						PromotionConfiguration: &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{{
								Name:      "current-release",
								Namespace: "ocp",
								Disabled:  true,
							}},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "current-release",
								Namespace: "ocp",
							},
							BaseImages: map[string]api.ImageStreamTagReference{
								"first": {
									Name:      "current-release",
									Namespace: "ocp",
									Tag:       "first",
								},
							},
							BaseRPMImages: map[string]api.ImageStreamTagReference{
								"second": {
									Name:      "current-release",
									Namespace: "ocp",
									Tag:       "second",
								},
							},
							BuildRootImage: &api.BuildRootImageConfiguration{
								ImageStreamTagReference: &api.ImageStreamTagReference{
									Name:      "current-release",
									Namespace: "ocp",
									Tag:       "third",
								},
							},
						},
					},
					Info: config.Info{
						Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "release-current-release"},
					},
				},
				{
					Configuration: api.ReleaseBuildConfiguration{
						Tests: []api.TestStepConfiguration{
							{As: "periodic-cron-portable", Cron: &cron, Portable: true},
						},
						PromotionConfiguration: &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{{
								Name:      "future-release-1",
								Namespace: "ocp",
							}},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "future-release-1",
								Namespace: "ocp",
							},
							BaseImages: map[string]api.ImageStreamTagReference{
								"first": {
									Name:      "future-release-1",
									Namespace: "ocp",
									Tag:       "first",
								},
							},
							BaseRPMImages: map[string]api.ImageStreamTagReference{
								"second": {
									Name:      "future-release-1",
									Namespace: "ocp",
									Tag:       "second",
								},
							},
							BuildRootImage: &api.BuildRootImageConfiguration{
								ImageStreamTagReference: &api.ImageStreamTagReference{
									Name:      "future-release-1",
									Namespace: "ocp",
									Tag:       "third",
								},
							},
						},
					},
					Info: config.Info{
						Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "release-future-release-1"},
					},
				},
				{
					Configuration: api.ReleaseBuildConfiguration{
						Tests: []api.TestStepConfiguration{
							{As: "periodic-cron-portable", Cron: &cron, Portable: true},
						},
						PromotionConfiguration: &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{{
								Name:      "future-release-2",
								Namespace: "ocp",
							}},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "future-release-2",
								Namespace: "ocp",
							},
							BaseImages: map[string]api.ImageStreamTagReference{
								"first": {
									Name:      "future-release-2",
									Namespace: "ocp",
									Tag:       "first",
								},
							},
							BaseRPMImages: map[string]api.ImageStreamTagReference{
								"second": {
									Name:      "future-release-2",
									Namespace: "ocp",
									Tag:       "second",
								},
							},
							BuildRootImage: &api.BuildRootImageConfiguration{
								ImageStreamTagReference: &api.ImageStreamTagReference{
									Name:      "future-release-2",
									Namespace: "ocp",
									Tag:       "third",
								},
							},
						},
					},
					Info: config.Info{
						Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "release-future-release-2"},
					},
				},
			},
		},
		{
			name:           "previously branched config that promotes to the current release from master bumps to the future release and de-mirrors correctly",
			currentRelease: "current-release",
			bumpRelease:    "future-release-1",
			futureReleases: []string{"current-release", "future-release-1", "future-release-2"},
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Targets: []api.PromotionTarget{{
							Name:      "current-release",
							Namespace: "ocp",
						}},
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
					},
				},
				Info: config.Info{
					Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "master"},
				},
			},
			output: []config.DataWithInfo{
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{{
								Name:      "future-release-1",
								Namespace: "ocp",
							}},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "future-release-1",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "master"},
					},
				},
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{{
								Name:      "current-release",
								Namespace: "ocp",
							}},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "current-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "release-current-release"},
					},
				},
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{{
								Name:      "future-release-1",
								Namespace: "ocp",
								Disabled:  true,
							}},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "future-release-1",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "release-future-release-1"},
					},
				},
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{{
								Name:      "future-release-2",
								Namespace: "ocp",
							}},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "future-release-2",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "release-future-release-2"},
					},
				},
			},
		},
		{
			name:           "remove additional targets that don't promote to the current release",
			currentRelease: "current-release",
			futureReleases: []string{"future-release"},
			input: config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Targets: []api.PromotionTarget{
							{
								Tag:       "target-1-tag",
								Namespace: "target-1-namespace",
							},
							{
								Name:      "current-release",
								Namespace: "target-2-namespace",
							},
						},
					},
					InputConfiguration: api.InputConfiguration{
						ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
							Name:      "current-release",
							Namespace: "ocp",
						},
					},
				},
				Info: config.Info{
					Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "master"},
				},
			},
			output: []config.DataWithInfo{
				{
					Configuration: api.ReleaseBuildConfiguration{
						PromotionConfiguration: &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{
								{
									Name:      "future-release",
									Namespace: "target-2-namespace",
								},
							},
						},
						InputConfiguration: api.InputConfiguration{
							ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
								Name:      "future-release",
								Namespace: "ocp",
							},
						},
					},
					Info: config.Info{
						Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "release-future-release"},
					},
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, expected := generateBranchedConfigs(testCase.currentRelease, testCase.bumpRelease, testCase.futureReleases, testCase.input, testCase.skipPeriodics), testCase.output
			if len(actual) != len(expected) {
				t.Fatalf("%s: did not generate correct amount of output configs, needed %d got %d", testCase.name, len(expected), len(actual))
			}
			for i := range expected {
				if !reflect.DeepEqual(actual[i].Info, expected[i].Info) {
					t.Errorf("%s: [%d] got incorrect path elements: %v", testCase.name, i, diff.ObjectReflectDiff(actual[i].Info, expected[i].Info))
				}
				if !reflect.DeepEqual(actual[i].Configuration.PromotionConfiguration, expected[i].Configuration.PromotionConfiguration) {
					t.Errorf("%s: [%d] got incorrect promotion config: %v", testCase.name, i, diff.ObjectReflectDiff(actual[i].Configuration.PromotionConfiguration, expected[i].Configuration.PromotionConfiguration))
				}
				if !reflect.DeepEqual(actual[i].Configuration.ReleaseTagConfiguration, expected[i].Configuration.ReleaseTagConfiguration) {
					t.Errorf("%s: [%d] got incorrect release input config: %v", testCase.name, i, diff.ObjectReflectDiff(actual[i].Configuration.ReleaseTagConfiguration, expected[i].Configuration.ReleaseTagConfiguration))
				}
				if !reflect.DeepEqual(actual[i].Configuration.Tests, expected[i].Configuration.Tests) {
					t.Errorf("%s: [%d] got incorrect test listing: %v", testCase.name, i, diff.ObjectReflectDiff(actual[i].Configuration.Tests, expected[i].Configuration.Tests))
				}
			}
		})
	}
}

func TestOptions_Bind(t *testing.T) {
	var testCases = []struct {
		name               string
		input              []string
		expected           options
		expectedFutureOpts []string
	}{
		{
			name:  "nothing set has defaults",
			input: []string{},
			expected: options{
				FutureOptions: promotion.FutureOptions{
					Options: promotion.Options{
						ConfirmableOptions: config.ConfirmableOptions{
							Options: config.Options{
								LogLevel: "info",
							},
						},
					},
				},
			},
		},
		{
			name: "everything set",
			input: []string{
				"--config-dir=foo",
				"--org=bar",
				"--repo=baz",
				"--log-level=debug",
				"--confirm",
				"--current-release=one",
				"--future-release=two",
				"--bump-release=three",
			},
			expected: options{
				FutureOptions: promotion.FutureOptions{
					Options: promotion.Options{
						ConfirmableOptions: config.ConfirmableOptions{
							Options: config.Options{
								ConfigDir: "foo",
								Org:       "bar",
								Repo:      "baz",
								LogLevel:  "debug",
							},
							Confirm: true},
						CurrentRelease: "one",
					},
					FutureReleases: flagutil.Strings{},
				},
				BumpRelease: "three",
			},
			expectedFutureOpts: []string{"two"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var o options
			fs := flag.NewFlagSet(testCase.name, flag.PanicOnError)
			o.Bind(fs)
			if err := fs.Parse(testCase.input); err != nil {
				t.Fatalf("%s: cannot parse args: %v", testCase.name, err)
			}
			expected := testCase.expected
			// this is not exposed for testing
			for _, opt := range testCase.expectedFutureOpts {
				if err := expected.FutureReleases.Set(opt); err != nil {
					t.Errorf("setting future release failed: %v", err)
				}
			}
			if actual, expected := o, expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect options: expected %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}
