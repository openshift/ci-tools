package api_test

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/validation"
)

// TestWithPresubmitFromInjectsProjectImageDependency is a regression test for the
// scenario behind /payload-job-with-prs runs that use --with-test-from to borrow a
// test which itself depends on a `pipeline:<image>` built by the source config (as
// opposed to one already present in the base config, or a base/release image).
//
// Before the fix, WithPresubmitFrom copied over BaseImages and Releases from the
// source config but silently dropped its Images, so the resulting configuration
// failed ci-operator's "loading_args:validating_config" step with:
//
//	tests[0].literal_steps.test[0].dependencies[0]: cannot determine source for
//	dependency "pipeline:vcf-migration-operator" - no base image import, project
//	image build, or bundle image build is configured to provide this dependency
func TestWithPresubmitFromInjectsProjectImageDependency(t *testing.T) {
	source := &api.ReleaseBuildConfiguration{
		Metadata: api.Metadata{Org: "openshift", Repo: "vcf-migration-operator", Branch: "main", Variant: "4.21"},
		InputConfiguration: api.InputConfiguration{
			BuildRootImage: &api.BuildRootImageConfiguration{FromRepository: true},
		},
		Images: api.ImageConfiguration{
			Items: []api.ProjectDirectoryImageBuildStepConfiguration{
				{
					To:                               "vcf-migration-operator",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile"},
				},
			},
		},
		Tests: []api.TestStepConfiguration{
			{
				As:   "e2e-vsphere-vcf-migration",
				Cron: strPtr("0 0 1 9 *"),
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Test: []api.LiteralTestStep{
						{
							As:       "run-e2e",
							From:     "src",
							Commands: "true",
							Resources: api.ResourceRequirements{
								Requests: api.ResourceList{"cpu": "100m"},
							},
							Dependencies: []api.StepDependency{
								{Env: "VCF_MIGRATION_OPERATOR_IMAGE", Name: "pipeline:vcf-migration-operator"},
							},
						},
					},
				},
			},
		},
	}

	base := &api.ReleaseBuildConfiguration{
		Metadata:  api.Metadata{Org: "openshift", Repo: "cluster-cloud-controller-manager-operator", Branch: "master"},
		Resources: api.ResourceConfiguration{"*": {Requests: api.ResourceList{"cpu": "100m", "memory": "200Mi"}}},
	}

	merged, err := base.WithPresubmitFrom(source, "e2e-vsphere-vcf-migration")
	if err != nil {
		t.Fatalf("WithPresubmitFrom returned an unexpected error: %v", err)
	}

	wantImageTo := api.PipelineImageStreamTagReference("vcf-migration-operator-openshift.vcf-migration-operator")
	wantImageFrom := api.PipelineImageStreamTagReference("src-openshift.vcf-migration-operator")
	if len(merged.Images.Items) != 1 {
		t.Fatalf("expected exactly one image to be carried over, got %d: %+v", len(merged.Images.Items), merged.Images.Items)
	}
	if merged.Images.Items[0].To != wantImageTo {
		t.Errorf("expected image 'to' %q, got %q", wantImageTo, merged.Images.Items[0].To)
	}
	if merged.Images.Items[0].From != wantImageFrom {
		t.Errorf("expected image 'from' %q, got %q", wantImageFrom, merged.Images.Items[0].From)
	}
	if merged.Images.Items[0].Ref != "openshift.vcf-migration-operator" {
		t.Errorf("expected image ref 'openshift.vcf-migration-operator', got %q", merged.Images.Items[0].Ref)
	}
	if got, ok := merged.BuildRootImages["openshift.vcf-migration-operator"]; !ok || !got.FromRepository {
		t.Errorf("expected a from_repository build root under 'openshift.vcf-migration-operator', got %+v (ok=%v)", got, ok)
	}

	gotDependency := merged.Tests[0].MultiStageTestConfigurationLiteral.Test[0].Dependencies[0].Name
	wantDependency := "pipeline:vcf-migration-operator-openshift.vcf-migration-operator"
	if gotDependency != wantDependency {
		t.Errorf("expected dependency name %q, got %q", wantDependency, gotDependency)
	}

	merged.Default()
	if err := validation.IsValidResolvedConfiguration(merged, true); err != nil {
		t.Errorf("expected the merged, injected-test configuration to be valid, got: %v", err)
	}

	sourceDependency := source.Tests[0].MultiStageTestConfigurationLiteral.Test[0].Dependencies[0].Name
	if sourceDependency != "pipeline:vcf-migration-operator" {
		t.Errorf("source configuration was mutated: expected dependency name unchanged at %q, got %q", "pipeline:vcf-migration-operator", sourceDependency)
	}
}

// TestWithPresubmitFromDoesNotMutateSourceDependenciesMap covers the workflow-based
// (non-literal) shape of a test, where dependency renaming writes into
// MultiStageTestConfiguration.Dependencies, a map shared by reference with source's
// test unless the test is deep-copied first. Source configs can be shared/cached
// objects served to concurrent requests (see configAgent.GetMatchingConfig), so
// mutating one in place here would corrupt state visible to unrelated callers.
func TestWithPresubmitFromDoesNotMutateSourceDependenciesMap(t *testing.T) {
	source := &api.ReleaseBuildConfiguration{
		Metadata: api.Metadata{Org: "openshift", Repo: "vcf-migration-operator", Branch: "main"},
		Images: api.ImageConfiguration{
			Items: []api.ProjectDirectoryImageBuildStepConfiguration{{To: "vcf-migration-operator"}},
		},
		Tests: []api.TestStepConfiguration{
			{
				As: "e2e-vsphere-vcf-migration",
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					Dependencies: api.TestDependencies{"VCF_MIGRATION_OPERATOR_IMAGE": "pipeline:vcf-migration-operator"},
				},
			},
		},
	}

	base := &api.ReleaseBuildConfiguration{}
	if _, err := base.WithPresubmitFrom(source, "e2e-vsphere-vcf-migration"); err != nil {
		t.Fatalf("WithPresubmitFrom returned an unexpected error: %v", err)
	}

	got := source.Tests[0].MultiStageTestConfiguration.Dependencies["VCF_MIGRATION_OPERATOR_IMAGE"]
	if got != "pipeline:vcf-migration-operator" {
		t.Errorf("source configuration's dependencies map was mutated: expected %q, got %q", "pipeline:vcf-migration-operator", got)
	}
}

func strPtr(s string) *string { return &s }
