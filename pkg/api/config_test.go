package api

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

// identityResolve is a resolve function for tests whose selected test has no
// workflow/chain/ref to expand (it is already literal, or has no multi-stage
// configuration at all), so resolving it is a no-op.
func identityResolve(config ReleaseBuildConfiguration) (ReleaseBuildConfiguration, error) {
	return config, nil
}

func TestWithPresubmitFrom(t *testing.T) {
	baseReleaseTagConfiguration := ReleaseTagConfiguration{Namespace: "base-namespace", Name: "base-is"}
	sourceReleaseTagConfiguration := ReleaseTagConfiguration{Namespace: "source-namespace", Name: "source-is"}
	baseTest := TestStepConfiguration{As: "base-test"}
	sourceTest := TestStepConfiguration{As: "source-test"}
	baseBaseImages := map[string]ImageStreamTagReference{"base-image": {
		Namespace: "base-namespace",
		Name:      "base-image",
		Tag:       "base-tag",
	}}
	sourceBaseImages := map[string]ImageStreamTagReference{"source-image": {
		Namespace: "source-namespace",
		Name:      "source-image",
		Tag:       "source-tag",
	}}
	baseImage := ProjectDirectoryImageBuildStepConfiguration{From: "base-image", To: "some-image"}
	sourceImage := ProjectDirectoryImageBuildStepConfiguration{From: "source-image", To: "other-image"}

	testCases := []struct {
		name   string
		base   *ReleaseBuildConfiguration
		source *ReleaseBuildConfiguration
		test   string
		// resolve defaults to identityResolve when nil
		resolve func(ReleaseBuildConfiguration) (ReleaseBuildConfiguration, error)

		// this is a shortcut to avoid repeating standard source/expected output
		// tests for testcases that check struct members unrelated to tests
		defaultTests bool

		expected      *ReleaseBuildConfiguration
		expectedError error
		// check, if set, runs additional assertions against the actual result and
		// the (unmodified) source configuration passed in
		check func(t *testing.T, actual *ReleaseBuildConfiguration, source *ReleaseBuildConfiguration)
	}{
		{
			name:     "selected test from source is present in result",
			base:     &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:   &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
			expected: &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
		},
		{
			name:          "error when selected test is not found in source",
			test:          "nonexistent",
			base:          &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:        &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
			expectedError: errors.New("test 'nonexistent' not found in source configuration"),
		},
		{
			name:     "selected test from source is present in result with interval stripped",
			base:     &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:   &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{{As: "source-test", Interval: pointer.StringPtr("24h")}}},
			expected: &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
		},
		{
			name:     "selected test from source is present in result with cron stripped",
			base:     &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:   &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{{As: "source-test", Cron: pointer.StringPtr("@hourly")}}},
			expected: &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
		},
		{
			name:     "selected test from source is present in result with postsubmit stripped away",
			base:     &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{baseTest}},
			source:   &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{{As: "source-test", Postsubmit: true}}},
			expected: &ReleaseBuildConfiguration{Tests: []TestStepConfiguration{sourceTest}},
		},
		{
			name:         "tag_specification from base is kept",
			base:         &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &baseReleaseTagConfiguration}},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &sourceReleaseTagConfiguration}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &baseReleaseTagConfiguration}},
			defaultTests: true,
		},
		{
			name:         "images from base is kept",
			base:         &ReleaseBuildConfiguration{Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{baseImage}}},
			source:       &ReleaseBuildConfiguration{Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{sourceImage}}},
			expected:     &ReleaseBuildConfiguration{Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{baseImage}}},
			defaultTests: true,
		},
		{
			name: "build_root from base is kept",
			base: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BuildRootImage: &BuildRootImageConfiguration{FromRepository: true}}},
			source: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BuildRootImage: &BuildRootImageConfiguration{
				ImageStreamTagReference: &ImageStreamTagReference{Namespace: "source", Name: "source-is", Tag: "source-tag"},
				UseBuildCache:           true,
			}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BuildRootImage: &BuildRootImageConfiguration{FromRepository: true}}},
			defaultTests: true,
		},
		{
			name:   "base_images is an union of both configs",
			base:   &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BaseImages: baseBaseImages}},
			source: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BaseImages: sourceBaseImages}},
			expected: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BaseImages: map[string]ImageStreamTagReference{
						"base-image":   {Namespace: "base-namespace", Name: "base-image", Tag: "base-tag"},
						"source-image": {Namespace: "source-namespace", Name: "source-image", Tag: "source-tag"},
					}},
			},
			defaultTests: true,
		},
		{
			name: "base_images do not conflict when both configs have a same base image",
			base: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BaseImages: baseBaseImages}},
			source: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{BaseImages: baseBaseImages},
				Tests:              []TestStepConfiguration{sourceTest},
			},
			expected: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BaseImages: map[string]ImageStreamTagReference{
						"base-image": {Namespace: "base-namespace", Name: "base-image", Tag: "base-tag"},
					}},
			},
			defaultTests: true,
		},
		{
			name: "errors when base_images conflict",
			base: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{BaseImages: baseBaseImages}},
			source: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{BaseImages: map[string]ImageStreamTagReference{"base-image": {
					Namespace: "another-namespace",
					Name:      "base-image",
					Tag:       "base-tag",
				}},
				},
				Tests: []TestStepConfiguration{sourceTest},
			},
			expectedError: errors.New("conflicting base_images: base-image"),
		},
		{
			name:   "release from source is present in result",
			base:   &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"base-release": {Release: &Release{Version: "4.9.base"}}}}},
			source: &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"source-release": {Release: &Release{Version: "4.9.source"}}}}},
			expected: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{
					"base-release":   {Release: &Release{Version: "4.9.base"}},
					"source-release": {Release: &Release{Version: "4.9.source"}}},
				},
			},
			defaultTests: true,
		},
		{
			name:         "release from source is overwrites the one with same name from base",
			base:         &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"source-release": {Release: &Release{Version: "4.9.base"}}}}},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"source-release": {Release: &Release{Version: "4.9.source"}}}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"source-release": {Release: &Release{Version: "4.9.source"}}}}},
			defaultTests: true,
		},
		{
			name:         "latest release from source is added if base does not have tag_specification",
			base:         &ReleaseBuildConfiguration{},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"latest": {Release: &Release{Version: "4.9.source"}}}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"latest": {Release: &Release{Version: "4.9.source"}}}}},
			defaultTests: true,
		},
		{
			name:         "latest release from source is not added if base has tag_specification",
			base:         &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &baseReleaseTagConfiguration}},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"latest": {Release: &Release{Version: "4.9.source"}}}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{ReleaseTagConfiguration: &baseReleaseTagConfiguration}},
			defaultTests: true,
		},
		{
			name:         "latest release from source is not added if base has releases.latest",
			base:         &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{LatestReleaseName: {Release: &Release{Version: "4.9.base"}}}}},
			source:       &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{"latest": {Release: &Release{Version: "4.9.source"}}}}},
			expected:     &ReleaseBuildConfiguration{InputConfiguration: InputConfiguration{Releases: map[string]UnresolvedRelease{LatestReleaseName: {Release: &Release{Version: "4.9.base"}}}}},
			defaultTests: true,
		},
		{
			name:         "enable_secrets_store_csi_driver from source is added to a base without prowgen",
			base:         &ReleaseBuildConfiguration{},
			source:       &ReleaseBuildConfiguration{Prowgen: &ProwgenOverrides{EnableSecretsStoreCSIDriver: true}},
			expected:     &ReleaseBuildConfiguration{Prowgen: &ProwgenOverrides{EnableSecretsStoreCSIDriver: true}},
			defaultTests: true,
		},
		{
			name:         "enable_secrets_store_csi_driver from source does not clobber other prowgen options in base",
			base:         &ReleaseBuildConfiguration{Prowgen: &ProwgenOverrides{Private: true}},
			source:       &ReleaseBuildConfiguration{Prowgen: &ProwgenOverrides{EnableSecretsStoreCSIDriver: true}},
			expected:     &ReleaseBuildConfiguration{Prowgen: &ProwgenOverrides{Private: true, EnableSecretsStoreCSIDriver: true}},
			defaultTests: true,
		},
		{
			name:         "prowgen options other than enable_secrets_store_csi_driver are not taken from source",
			base:         &ReleaseBuildConfiguration{},
			source:       &ReleaseBuildConfiguration{Prowgen: &ProwgenOverrides{Private: true}},
			expected:     &ReleaseBuildConfiguration{},
			defaultTests: true,
		},
		{
			name:         "enable_secrets_store_csi_driver from base is kept when source does not set it",
			base:         &ReleaseBuildConfiguration{Prowgen: &ProwgenOverrides{EnableSecretsStoreCSIDriver: true}},
			source:       &ReleaseBuildConfiguration{},
			expected:     &ReleaseBuildConfiguration{Prowgen: &ProwgenOverrides{EnableSecretsStoreCSIDriver: true}},
			defaultTests: true,
		},
		// The following cases are regression tests for /payload-job-with-prs runs that use
		// --with-test-from to borrow a test which itself depends on a `pipeline:<image>`
		// built by the source config (as opposed to one already present in the base config,
		// or a base/release image). Before the underlying fix, WithPresubmitFrom copied over
		// BaseImages and Releases from the source config but silently dropped its Images,
		// failing ci-operator's "loading_args:validating_config" step with:
		//
		//	tests[0].literal_steps.test[0].dependencies[0]: cannot determine source for
		//	dependency "pipeline:vcf-migration-operator" - no base image import, project
		//	image build, or bundle image build is configured to provide this dependency
		{
			name: "project image dependency (and its build root) is carried over from source, namespaced by its repo",
			base: &ReleaseBuildConfiguration{Metadata: Metadata{Org: "openshift", Repo: "cluster-cloud-controller-manager-operator", Branch: "master"}},
			source: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "openshift", Repo: "vcf-migration-operator", Branch: "main", Variant: "4.21"},
				InputConfiguration: InputConfiguration{
					BuildRootImage: &BuildRootImageConfiguration{FromRepository: true},
				},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "vcf-migration-operator", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{
							As: "run-e2e", From: "src", Commands: "true",
							Dependencies: []StepDependency{{Env: "VCF_MIGRATION_OPERATOR_IMAGE", Name: "pipeline:vcf-migration-operator"}},
						}},
					},
				}},
			},
			test: "e2e",
			expected: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "openshift", Repo: "cluster-cloud-controller-manager-operator", Branch: "master"},
				InputConfiguration: InputConfiguration{
					BuildRootImages: map[string]BuildRootImageConfiguration{"openshift.vcf-migration-operator": {FromRepository: true}},
				},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{
						To: "vcf-migration-operator-openshift.vcf-migration-operator", Ref: "openshift.vcf-migration-operator",
						ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"},
					},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{
							As: "run-e2e", From: "src", Commands: "true",
							Dependencies: []StepDependency{{Env: "VCF_MIGRATION_OPERATOR_IMAGE", Name: "pipeline:vcf-migration-operator-openshift.vcf-migration-operator"}},
						}},
					},
				}},
			},
		},
		{
			name: "a dependency reached only via inputs, not from, is carried over, and its inputs key renamed",
			base: &ReleaseBuildConfiguration{Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"}},
			source: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "org", Repo: "source", Branch: "main"},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "helper", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "helper.Dockerfile"}},
					{To: "app", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{
						DockerfilePath: "app.Dockerfile",
						Inputs:         map[string]ImageBuildInputs{"helper": {Paths: []ImageSourcePath{{SourcePath: "/output/tool", DestinationDir: "."}}}},
					}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app"}}}},
					},
				}},
			},
			test: "e2e",
			expected: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "helper-org.source", Ref: "org.source", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "helper.Dockerfile"}},
					{To: "app-org.source", Ref: "org.source", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{
						DockerfilePath: "app.Dockerfile",
						Inputs:         map[string]ImageBuildInputs{"helper-org.source": {Paths: []ImageSourcePath{{SourcePath: "/output/tool", DestinationDir: "."}}}},
					}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app-org.source"}}}},
					},
				}},
			},
		},
		{
			name: "from is left empty when source's image had none (its Dockerfile sets its own base image)",
			base: &ReleaseBuildConfiguration{Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"}},
			source: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "org", Repo: "source", Branch: "main"},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app"}}}},
					},
				}},
			},
			test: "e2e",
			expected: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app-org.source", Ref: "org.source", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app-org.source"}}}},
					},
				}},
			},
		},
		{
			name: "a from: bin parent is namespaced and source's binary build commands are carried over under the same ref",
			base: &ReleaseBuildConfiguration{Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"}},
			source: &ReleaseBuildConfiguration{
				Metadata:            Metadata{Org: "org", Repo: "source", Branch: "main"},
				BinaryBuildCommands: "make build",
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app", From: "bin", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app"}}}},
					},
				}},
			},
			test: "e2e",
			expected: &ReleaseBuildConfiguration{
				Metadata:                Metadata{Org: "org", Repo: "component", Branch: "main"},
				BinaryBuildCommandsList: []RefCommands{{Ref: "org.source", Commands: "make build"}},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app-org.source", From: "bin-org.source", Ref: "org.source", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app-org.source"}}}},
					},
				}},
			},
		},
		{
			name: "a from referring to an imported base image alias is left unchanged: the alias is already carried over as-is",
			base: &ReleaseBuildConfiguration{Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"}},
			source: &ReleaseBuildConfiguration{
				Metadata:           Metadata{Org: "org", Repo: "source", Branch: "main"},
				InputConfiguration: InputConfiguration{BaseImages: map[string]ImageStreamTagReference{"base": {Namespace: "ns", Name: "base", Tag: "latest"}}},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app", From: "base", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app"}}}},
					},
				}},
			},
			test: "e2e",
			expected: &ReleaseBuildConfiguration{
				Metadata:           Metadata{Org: "org", Repo: "component", Branch: "main"},
				InputConfiguration: InputConfiguration{BaseImages: map[string]ImageStreamTagReference{"base": {Namespace: "ns", Name: "base", Tag: "latest"}}},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app-org.source", From: "base", Ref: "org.source", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app-org.source"}}}},
					},
				}},
			},
		},
		{
			name: "source's build root is deep-copied, not aliased, when carried over",
			base: &ReleaseBuildConfiguration{Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"}},
			source: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "org", Repo: "source", Branch: "main"},
				InputConfiguration: InputConfiguration{
					BuildRootImage: &BuildRootImageConfiguration{ProjectImageBuild: &ProjectDirectoryImageBuildInputs{DockerfilePath: "root.Dockerfile"}},
				},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app"}}}},
					},
				}},
			},
			test: "e2e",
			expected: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"},
				InputConfiguration: InputConfiguration{
					BuildRootImages: map[string]BuildRootImageConfiguration{
						"org.source": {ProjectImageBuild: &ProjectDirectoryImageBuildInputs{DockerfilePath: "root.Dockerfile"}},
					},
				},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app-org.source", Ref: "org.source", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run", Dependencies: []StepDependency{{Env: "APP", Name: "pipeline:app-org.source"}}}},
					},
				}},
			},
			check: func(t *testing.T, actual *ReleaseBuildConfiguration, source *ReleaseBuildConfiguration) {
				actual.BuildRootImages["org.source"].ProjectImageBuild.Ref = "mutated"
				if source.BuildRootImage.ProjectImageBuild.Ref == "mutated" {
					t.Errorf("mutating the carried-over build root also mutated source's: the build root was not deep-copied")
				}
			},
		},
		{
			name: "a dependency hidden inside the test's not-yet-resolved workflow is discovered and carried over",
			base: &ReleaseBuildConfiguration{Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"}},
			source: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "org", Repo: "source", Branch: "main"},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As:                          "e2e",
					MultiStageTestConfiguration: &MultiStageTestConfiguration{Workflow: pointer.StringPtr("example-e2e")},
				}},
			},
			test: "e2e",
			// Simulates what the real step registry resolver does: expands the test's
			// workflow (and any chains/references within it) into literal steps, surfacing
			// a dependency that was invisible while the test only referenced the workflow
			// by name.
			resolve: func(config ReleaseBuildConfiguration) (ReleaseBuildConfiguration, error) {
				var tests []TestStepConfiguration
				for _, t := range config.Tests {
					if wf := t.MultiStageTestConfiguration; wf != nil && wf.Workflow != nil && *wf.Workflow == "example-e2e" {
						t.MultiStageTestConfiguration = nil
						t.MultiStageTestConfigurationLiteral = &MultiStageTestConfigurationLiteral{
							Test: []LiteralTestStep{{As: "run-e2e", Dependencies: []StepDependency{{Env: "APP_IMAGE", Name: "pipeline:app"}}}},
						}
					}
					tests = append(tests, t)
				}
				config.Tests = tests
				return config, nil
			},
			expected: &ReleaseBuildConfiguration{
				Metadata: Metadata{Org: "org", Repo: "component", Branch: "main"},
				Images: ImageConfiguration{Items: []ProjectDirectoryImageBuildStepConfiguration{
					{To: "app-org.source", Ref: "org.source", ProjectDirectoryImageBuildInputs: ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"}},
				}},
				Tests: []TestStepConfiguration{{
					As: "e2e",
					MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
						Test: []LiteralTestStep{{As: "run-e2e", Dependencies: []StepDependency{{Env: "APP_IMAGE", Name: "pipeline:app-org.source"}}}},
					},
				}},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.test == "" {
				tc.test = "source-test"
			}
			if tc.defaultTests {
				tc.source.Tests = []TestStepConfiguration{sourceTest}
				tc.expected.Tests = []TestStepConfiguration{sourceTest}
			}
			resolve := tc.resolve
			if resolve == nil {
				resolve = identityResolve
			}
			sourceBeforeInjection := tc.source.DeepCopy()

			actual, err := tc.base.WithPresubmitFrom(tc.source, tc.test, resolve)

			if errDiff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); errDiff != "" {
				t.Errorf("Error differs from expected:\n%s", errDiff)
			}

			if diff := cmp.Diff(tc.expected, actual, cmpopts.IgnoreUnexported(ProjectDirectoryImageBuildStepConfiguration{})); tc.expectedError == nil && diff != "" {
				t.Errorf("Result differs from expected:\n%s", diff)
			}

			if diff := cmp.Diff(sourceBeforeInjection, tc.source, cmpopts.IgnoreUnexported(ProjectDirectoryImageBuildStepConfiguration{})); diff != "" {
				t.Errorf("source configuration was mutated:\n%s", diff)
			}

			if tc.check != nil {
				tc.check(t, actual, tc.source)
			}
		})
	}
}
