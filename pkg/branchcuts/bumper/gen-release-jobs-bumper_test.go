package bumper

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	cioperatorcfg "github.com/openshift/ci-tools/pkg/config"
)

func TestBumpFilename(t *testing.T) {
	tests := []struct {
		id           string
		ocpRelease   string
		meta         cioperatorapi.Metadata
		wantFilename string
	}{
		{
			id:         "Bump filename properly",
			ocpRelease: "4.11",
			meta: cioperatorapi.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "br",
				Variant: "nightly-4.10",
			},
			wantFilename: "org-repo-br__nightly-4.11.yaml",
		},
		{
			id:         "Bump nothing",
			ocpRelease: "4.11",
			meta: cioperatorapi.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "br",
				Variant: "nightly-5",
			},
			wantFilename: "org-repo-br__nightly-5.yaml",
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			b, err := NewGeneratedReleaseGatingJobsBumper(test.ocpRelease, "", 1)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			dataWithInfo := cioperatorcfg.DataWithInfo{
				Info: cioperatorcfg.Info{
					Metadata: test.meta,
				},
			}
			filename, err := b.BumpFilename("", &dataWithInfo)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if test.wantFilename != filename {
				t.Errorf("filenames are different: %s", cmp.Diff(test.wantFilename, filename))
			}
		})
	}
}

func TestBumpObject(t *testing.T) {
	strRef := func(i string) *string {
		return &i
	}
	tests := []struct {
		id         string
		ocpRelease string
		interval   int
		config     cioperatorapi.ReleaseBuildConfiguration
		wantConfig cioperatorapi.ReleaseBuildConfiguration
	}{
		{
			id:         "Object bumped properly",
			ocpRelease: "4.11",
			interval:   168,
			config: cioperatorapi.ReleaseBuildConfiguration{
				Metadata: cioperatorapi.Metadata{
					Variant: "4.10",
				},
				InputConfiguration: cioperatorapi.InputConfiguration{
					BaseImages: map[string]cioperatorapi.ImageStreamTagReference{
						"image-1": {
							Name: "image_4.10",
						},
					},
					Releases: map[string]cioperatorapi.UnresolvedRelease{
						"release": {
							Release: &cioperatorapi.Release{
								Version: "4.10",
							},
						},
						"candidate": {
							Release: &cioperatorapi.Release{
								Version: "4.10",
							},
						},
						"prerelease": {
							Prerelease: &cioperatorapi.Prerelease{
								VersionBounds: cioperatorapi.VersionBounds{
									Lower: "4.9",
									Upper: "4.11",
								},
							},
						},
					},
				},
				Tests: []cioperatorapi.TestStepConfiguration{
					{
						Interval: strRef("24h"),
						MultiStageTestConfiguration: &cioperatorapi.MultiStageTestConfiguration{
							Environment: cioperatorapi.TestEnvironment{
								"OCP_VERSION":    "4.10",
								"Some_Other_VAR": "4.10",
							},
							Workflow: strRef("w-4.10-4.11"),
							Test: []cioperatorapi.TestStep{
								{
									LiteralTestStep: &cioperatorapi.LiteralTestStep{
										Environment: []cioperatorapi.StepParameter{
											{
												Name:    "OCP_VERSION",
												Default: strRef("4.10"),
											},
											{
												Name:    "Some_Other_VAR",
												Default: strRef("4.10"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantConfig: cioperatorapi.ReleaseBuildConfiguration{
				Metadata: cioperatorapi.Metadata{
					Variant: "4.11",
				},
				InputConfiguration: cioperatorapi.InputConfiguration{
					BaseImages: map[string]cioperatorapi.ImageStreamTagReference{
						"image-1": {
							Name: "image_4.11",
						},
					},
					Releases: map[string]cioperatorapi.UnresolvedRelease{
						"release": {
							Release: &cioperatorapi.Release{
								Version: "4.11",
							},
						},
						"candidate": {
							Release: &cioperatorapi.Release{
								Version: "4.11",
							},
						},
						"prerelease": {
							Prerelease: &cioperatorapi.Prerelease{
								VersionBounds: cioperatorapi.VersionBounds{
									Lower: "4.10",
									Upper: "4.12",
								},
							},
						},
					},
				},
				Tests: []cioperatorapi.TestStepConfiguration{
					{
						Interval: strRef("168h"),
						MultiStageTestConfiguration: &cioperatorapi.MultiStageTestConfiguration{
							Environment: cioperatorapi.TestEnvironment{
								"OCP_VERSION":    "4.11",
								"Some_Other_VAR": "4.11",
							},
							Workflow: strRef("w-4.11-4.12"),
							Test: []cioperatorapi.TestStep{
								{
									LiteralTestStep: &cioperatorapi.LiteralTestStep{
										Environment: []cioperatorapi.StepParameter{
											{
												Name:    "OCP_VERSION",
												Default: strRef("4.11"),
											},
											{
												Name:    "Some_Other_VAR",
												Default: strRef("4.11"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			b, err := NewGeneratedReleaseGatingJobsBumper(test.ocpRelease, "", test.interval)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			dataWithInfo := cioperatorcfg.DataWithInfo{
				Configuration: test.config,
			}
			result, err := b.BumpContent(&dataWithInfo)
			if err != nil {
				t.Errorf("Unexpected error: %s", err.Error())
			}
			if diff := cmp.Diff(&test.wantConfig, &result.Configuration); diff != "" {
				t.Errorf("Configurations are different: %s", diff)
			}
		})
	}
}
