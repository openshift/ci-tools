package bumper

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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
			b, err := NewGeneratedReleaseGatingJobsBumper(test.ocpRelease, "", 1, FileFinderSignal)
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
						SlackReporterConfig: &cioperatorapi.SlackReporterConfig{
							Channel: "#cnv-release-4-10-z",
						},
						MultiStageTestConfiguration: &cioperatorapi.MultiStageTestConfiguration{
							Workflow: strRef("w-4.10-4.11"),
							Test: []cioperatorapi.TestStep{
								{
									LiteralTestStep: &cioperatorapi.LiteralTestStep{
										Environment: []cioperatorapi.StepParameter{
											{
												Name:    "OCP_VERSION",
												Default: strRef("4.10"),
											},
										},
									},
								},
							},
						},
					},
				},
				Images: cioperatorapi.ImageConfiguration{
					Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
						{
							ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
								BuildArgs: []cioperatorapi.BuildArg{
									{Name: "OCP_VERSION", Value: "4.10"},
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
						SlackReporterConfig: &cioperatorapi.SlackReporterConfig{
							Channel: "#cnv-release-4-11-z",
						},
						MultiStageTestConfiguration: &cioperatorapi.MultiStageTestConfiguration{
							Workflow: strRef("w-4.11-4.12"),
							Test: []cioperatorapi.TestStep{
								{
									LiteralTestStep: &cioperatorapi.LiteralTestStep{
										Environment: []cioperatorapi.StepParameter{
											{
												Name:    "OCP_VERSION",
												Default: strRef("4.11"),
											},
										},
									},
								},
							},
						},
					},
				},
				Images: cioperatorapi.ImageConfiguration{
					Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
						{
							ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
								BuildArgs: []cioperatorapi.BuildArg{
									{Name: "OCP_VERSION", Value: "4.11"},
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
			b, err := NewGeneratedReleaseGatingJobsBumper(test.ocpRelease, "", test.interval, FileFinderSignal)
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
			if diff := cmp.Diff(&test.wantConfig, &result.Configuration, cmpopts.IgnoreUnexported(cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{})); diff != "" {
				t.Errorf("Configurations are different: %s", diff)
			}
		})
	}
}

func TestBumpStepEnvVars(t *testing.T) {
	tests := []struct {
		id      string
		major   int
		env     cioperatorapi.TestEnvironment
		wantEnv cioperatorapi.TestEnvironment
	}{
		{
			id:    "Bumps OCP_VERSION env var",
			major: 4,
			env: cioperatorapi.TestEnvironment{
				"OCP_VERSION": "4.10",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"OCP_VERSION": "4.11",
			},
		},
		{
			id:    "Bumps env var with matching major version",
			major: 4,
			env: cioperatorapi.TestEnvironment{
				"SOME_VERSION": "4.10",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"SOME_VERSION": "4.11",
			},
		},
		{
			id:    "Does not bump env var with different major version",
			major: 4,
			env: cioperatorapi.TestEnvironment{
				"OTHER_VERSION": "5.10",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"OTHER_VERSION": "5.10",
			},
		},
		{
			id:    "Does not bump non-version value",
			major: 4,
			env: cioperatorapi.TestEnvironment{
				"SOME_VAR": "not-a-version",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"SOME_VAR": "not-a-version",
			},
		},
		{
			id:    "Bumps multiple matching env vars",
			major: 4,
			env: cioperatorapi.TestEnvironment{
				"OCP_VERSION":   "4.10",
				"OTHER_VERSION": "4.9",
				"UNRELATED":     "foobar",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"OCP_VERSION":   "4.11",
				"OTHER_VERSION": "4.10",
				"UNRELATED":     "foobar",
			},
		},
		{
			id:      "Handles nil env",
			major:   4,
			env:     nil,
			wantEnv: nil,
		},
		// Fallback: embedded version in env var values
		{
			id:    "Bumps AGENT_ISO with embedded dot version",
			major: 5,
			env: cioperatorapi.TestEnvironment{
				"AGENT_ISO": "agent-ove-5.0.x86_64.iso",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"AGENT_ISO": "agent-ove-5.1.x86_64.iso",
			},
		},
		{
			id:    "Bumps TELEMETRY_GROUP with embedded dot version",
			major: 5,
			env: cioperatorapi.TestEnvironment{
				"TELEMETRY_GROUP": "prow-ocp-5.0-component-readiness",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"TELEMETRY_GROUP": "prow-ocp-5.1-component-readiness",
			},
		},
		{
			id:    "Bumps JOB_NAME with embedded dot version",
			major: 5,
			env: cioperatorapi.TestEnvironment{
				"JOB_NAME": "periodic-ci-openshift-release-master-nightly-5.0-e2e-aws",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"JOB_NAME": "periodic-ci-openshift-release-master-nightly-5.1-e2e-aws",
			},
		},
		{
			id:    "Bumps REPORTER_TEMPLATE_NAME with underscore version",
			major: 5,
			env: cioperatorapi.TestEnvironment{
				"REPORTER_TEMPLATE_NAME": "component_readiness_5_0",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"REPORTER_TEMPLATE_NAME": "component_readiness_5_1",
			},
		},
		{
			id:    "Bumps embedded hyphen version",
			major: 5,
			env: cioperatorapi.TestEnvironment{
				"CHANNEL": "cnv-release-5-0-z",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"CHANNEL": "cnv-release-5-1-z",
			},
		},
		{
			id:    "Does not bump embedded version with different major",
			major: 4,
			env: cioperatorapi.TestEnvironment{
				"AGENT_ISO": "agent-ove-5.0.x86_64.iso",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"AGENT_ISO": "agent-ove-5.0.x86_64.iso",
			},
		},
		{
			id:    "Bumps mix of bare and embedded versions",
			major: 5,
			env: cioperatorapi.TestEnvironment{
				"OCP_VERSION":   "5.0",
				"AGENT_ISO":     "agent-ove-5.0.x86_64.iso",
				"UNRELATED_VAR": "hello-world",
			},
			wantEnv: cioperatorapi.TestEnvironment{
				"OCP_VERSION":   "5.1",
				"AGENT_ISO":     "agent-ove-5.1.x86_64.iso",
				"UNRELATED_VAR": "hello-world",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			err := bumpStepEnvVars(test.env, test.major)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(test.wantEnv, test.env); diff != "" {
				t.Errorf("environments are different: %s", diff)
			}
		})
	}
}

func TestBumpTestStepEnvVars(t *testing.T) {
	strRef := func(i string) *string {
		return &i
	}
	tests := []struct {
		id       string
		major    int
		testStep cioperatorapi.TestStep
		wantStep cioperatorapi.TestStep
	}{
		{
			id:    "Bumps OCP_VERSION in step environment",
			major: 4,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "OCP_VERSION", Default: strRef("4.10")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "OCP_VERSION", Default: strRef("4.11")},
					},
				},
			},
		},
		{
			id:    "Bumps any env var with matching major version",
			major: 4,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "CUSTOM_VAR", Default: strRef("4.10")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "CUSTOM_VAR", Default: strRef("4.11")},
					},
				},
			},
		},
		{
			id:    "Does not bump env var with different major version",
			major: 4,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "OTHER_VAR", Default: strRef("5.10")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "OTHER_VAR", Default: strRef("5.10")},
					},
				},
			},
		},
		{
			id:    "Does not bump non-version value",
			major: 4,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "SOME_VAR", Default: strRef("not-a-version")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "SOME_VAR", Default: strRef("not-a-version")},
					},
				},
			},
		},
		{
			id:       "Handles nil LiteralTestStep",
			major:    4,
			testStep: cioperatorapi.TestStep{},
			wantStep: cioperatorapi.TestStep{},
		},
		{
			id:    "Handles nil Default field in StepParameter",
			major: 4,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "OCP_VERSION", Default: nil},
						{Name: "OTHER_VAR", Default: strRef("4.10")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "OCP_VERSION", Default: nil},
						{Name: "OTHER_VAR", Default: strRef("4.11")},
					},
				},
			},
		},
		// Fallback: embedded version in step parameter values
		{
			id:    "Bumps AGENT_ISO with embedded version in step param",
			major: 5,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "AGENT_ISO", Default: strRef("agent-ove-5.0.x86_64.iso")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "AGENT_ISO", Default: strRef("agent-ove-5.1.x86_64.iso")},
					},
				},
			},
		},
		{
			id:    "Bumps REPORTER_TEMPLATE_NAME with underscore version in step param",
			major: 5,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "REPORTER_TEMPLATE_NAME", Default: strRef("component_readiness_5_0")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "REPORTER_TEMPLATE_NAME", Default: strRef("component_readiness_5_1")},
					},
				},
			},
		},
		{
			id:    "Bumps TELEMETRY_GROUP with embedded version in step param",
			major: 5,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "TELEMETRY_GROUP", Default: strRef("prow-ocp-5.0-component-readiness")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "TELEMETRY_GROUP", Default: strRef("prow-ocp-5.1-component-readiness")},
					},
				},
			},
		},
		{
			id:    "Does not bump embedded version with different major in step param",
			major: 4,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "AGENT_ISO", Default: strRef("agent-ove-5.0.x86_64.iso")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "AGENT_ISO", Default: strRef("agent-ove-5.0.x86_64.iso")},
					},
				},
			},
		},
		{
			id:    "Bumps mix of bare and embedded versions in step params",
			major: 5,
			testStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "OCP_VERSION", Default: strRef("5.0")},
						{Name: "AGENT_ISO", Default: strRef("agent-ove-5.0.x86_64.iso")},
						{Name: "UNRELATED", Default: strRef("hello-world")},
					},
				},
			},
			wantStep: cioperatorapi.TestStep{
				LiteralTestStep: &cioperatorapi.LiteralTestStep{
					Environment: []cioperatorapi.StepParameter{
						{Name: "OCP_VERSION", Default: strRef("5.1")},
						{Name: "AGENT_ISO", Default: strRef("agent-ove-5.1.x86_64.iso")},
						{Name: "UNRELATED", Default: strRef("hello-world")},
					},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			err := bumpTestStepEnvVars(test.testStep, test.major)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(test.wantStep, test.testStep); diff != "" {
				t.Errorf("test steps are different: %s", diff)
			}
		})
	}
}

func TestBumpBaseImages(t *testing.T) {
	tests := []struct {
		id             string
		major          int
		baseImages     map[string]cioperatorapi.ImageStreamTagReference
		wantBaseImages map[string]cioperatorapi.ImageStreamTagReference
	}{
		{
			id:    "Bumps name and plain version tag",
			major: 4,
			baseImages: map[string]cioperatorapi.ImageStreamTagReference{
				"tests-private": {
					Name: "tests-private",
					Tag:  "4.10",
				},
			},
			wantBaseImages: map[string]cioperatorapi.ImageStreamTagReference{
				"tests-private": {
					Name: "tests-private",
					Tag:  "4.11",
				},
			},
		},
		{
			id:    "Bumps name with version, does not bump non-version tag",
			major: 4,
			baseImages: map[string]cioperatorapi.ImageStreamTagReference{
				"image": {
					Name: "image_4.10",
					Tag:  "latest",
				},
			},
			wantBaseImages: map[string]cioperatorapi.ImageStreamTagReference{
				"image": {
					Name: "image_4.11",
					Tag:  "latest",
				},
			},
		},
		{
			id:    "Does not bump tag with version as substring",
			major: 4,
			baseImages: map[string]cioperatorapi.ImageStreamTagReference{
				"golang": {
					Name: "golang",
					Tag:  "rhel-9-golang-1.24-openshift-4.10",
				},
			},
			wantBaseImages: map[string]cioperatorapi.ImageStreamTagReference{
				"golang": {
					Name: "golang",
					Tag:  "rhel-9-golang-1.24-openshift-4.10",
				},
			},
		},
		{
			id:    "Does not bump tag with different major version",
			major: 4,
			baseImages: map[string]cioperatorapi.ImageStreamTagReference{
				"image": {
					Name: "image",
					Tag:  "5.10",
				},
			},
			wantBaseImages: map[string]cioperatorapi.ImageStreamTagReference{
				"image": {
					Name: "image",
					Tag:  "5.10",
				},
			},
		},
		{
			id:             "Handles nil base images",
			major:          4,
			baseImages:     nil,
			wantBaseImages: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			config := &cioperatorapi.ReleaseBuildConfiguration{
				InputConfiguration: cioperatorapi.InputConfiguration{
					BaseImages: test.baseImages,
				},
			}
			err := bumpBaseImages(config, test.major)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(test.wantBaseImages, config.BaseImages); diff != "" {
				t.Errorf("base images are different: %s", diff)
			}
		})
	}
}

func TestBumpReporterConfig(t *testing.T) {
	tests := []struct {
		id          string
		major       int
		rc          *cioperatorapi.SlackReporterConfig
		wantChannel string
	}{
		{
			id:          "Bumps channel with hyphen version",
			major:       5,
			rc:          &cioperatorapi.SlackReporterConfig{Channel: "#cnv-release-5-0-z"},
			wantChannel: "#cnv-release-5-1-z",
		},
		{
			id:          "Bumps channel with dot version",
			major:       4,
			rc:          &cioperatorapi.SlackReporterConfig{Channel: "#ocp-4.10-alerts"},
			wantChannel: "#ocp-4.11-alerts",
		},
		{
			id:          "Bumps channel with underscore version",
			major:       5,
			rc:          &cioperatorapi.SlackReporterConfig{Channel: "#team_5_0_notifications"},
			wantChannel: "#team_5_1_notifications",
		},
		{
			id:          "Does not bump channel with different major",
			major:       4,
			rc:          &cioperatorapi.SlackReporterConfig{Channel: "#cnv-release-5-0-z"},
			wantChannel: "#cnv-release-5-0-z",
		},
		{
			id:          "Does not bump channel without version",
			major:       4,
			rc:          &cioperatorapi.SlackReporterConfig{Channel: "#general"},
			wantChannel: "#general",
		},
		{
			id:    "Handles nil reporter config",
			major: 4,
			rc:    nil,
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			err := bumpReporterConfig(test.rc, test.major)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if test.rc != nil {
				if diff := cmp.Diff(test.wantChannel, test.rc.Channel); diff != "" {
					t.Errorf("channels are different: %s", diff)
				}
			}
		})
	}
}

func TestBumpImages(t *testing.T) {
	tests := []struct {
		id         string
		major      int
		images     cioperatorapi.ImageConfiguration
		wantImages cioperatorapi.ImageConfiguration
	}{
		{
			id:    "Bumps exact version in build arg",
			major: 4,
			images: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "OCP_VERSION", Value: "4.10"},
							},
						},
					},
				},
			},
			wantImages: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "OCP_VERSION", Value: "4.11"},
							},
						},
					},
				},
			},
		},
		{
			id:    "Bumps embedded dot version in build arg",
			major: 5,
			images: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "IMAGE_TAG", Value: "registry.example.com/image:5.0-latest"},
							},
						},
					},
				},
			},
			wantImages: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "IMAGE_TAG", Value: "registry.example.com/image:5.1-latest"},
							},
						},
					},
				},
			},
		},
		{
			id:    "Bumps embedded underscore version in build arg",
			major: 5,
			images: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "CONFIG_NAME", Value: "config_5_0_release"},
							},
						},
					},
				},
			},
			wantImages: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "CONFIG_NAME", Value: "config_5_1_release"},
							},
						},
					},
				},
			},
		},
		{
			id:    "Does not bump build arg with different major",
			major: 4,
			images: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "VERSION", Value: "5.0"},
							},
						},
					},
				},
			},
			wantImages: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "VERSION", Value: "5.0"},
							},
						},
					},
				},
			},
		},
		{
			id:    "Does not bump non-version build arg",
			major: 4,
			images: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "SOME_ARG", Value: "not-a-version"},
							},
						},
					},
				},
			},
			wantImages: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "SOME_ARG", Value: "not-a-version"},
							},
						},
					},
				},
			},
		},
		{
			id:    "Bumps multiple build args across multiple images",
			major: 5,
			images: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "VERSION", Value: "5.0"},
								{Name: "TAG", Value: "build-5.0-rc1"},
							},
						},
					},
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "RELEASE", Value: "release_5_0"},
							},
						},
					},
				},
			},
			wantImages: cioperatorapi.ImageConfiguration{
				Items: []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "VERSION", Value: "5.1"},
								{Name: "TAG", Value: "build-5.1-rc1"},
							},
						},
					},
					{
						ProjectDirectoryImageBuildInputs: cioperatorapi.ProjectDirectoryImageBuildInputs{
							BuildArgs: []cioperatorapi.BuildArg{
								{Name: "RELEASE", Value: "release_5_1"},
							},
						},
					},
				},
			},
		},
		{
			id:         "Handles empty images",
			major:      4,
			images:     cioperatorapi.ImageConfiguration{},
			wantImages: cioperatorapi.ImageConfiguration{},
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			config := &cioperatorapi.ReleaseBuildConfiguration{
				Images: test.images,
			}
			err := bumpImages(config, test.major)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(test.wantImages, config.Images, cmpopts.IgnoreUnexported(cioperatorapi.ProjectDirectoryImageBuildStepConfiguration{})); diff != "" {
				t.Errorf("images are different: %s", diff)
			}
		})
	}
}
