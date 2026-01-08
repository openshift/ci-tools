package main

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/ocpbuilddata"
	"github.com/openshift/ci-tools/pkg/config"
	cidockerfile "github.com/openshift/ci-tools/pkg/dockerfile"
	"github.com/openshift/ci-tools/pkg/github"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestReplacer(t *testing.T) {
	majorMinor := ocpbuilddata.MajorMinor{Major: "4", Minor: "6"}
	testCases := []struct {
		name                                         string
		config                                       *api.ReleaseBuildConfiguration
		pruneUnusedReplacementsEnabled               bool
		pruneOCPBuilderReplacementsEnabled           bool
		pruneUnusedBaseImagesEnabled                 bool
		ensureCorrectPromotionDockerfile             bool
		ensureCorrectPromotionDockerfileIngoredRepos sets.Set[string]
		ignoreRepos                                  sets.Set[string]
		promotionTargetToDockerfileMapping           map[string]dockerfileLocation
		files                                        map[string][]byte
		credentials                                  *usernameToken
		expectWrite                                  bool
		epectedOpts                                  github.Opts
	}{
		{
			name: "No dockerfile, does nothing",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{}},
			},
		},
		{
			name: "Default to dockerfile",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{}},
			},
			files:       map[string][]byte{"Dockerfile": []byte("FROM registry.svc.ci.openshift.org/org/repo:tag")},
			expectWrite: true,
		},
		{
			name: "Use dockerfile_literal if present",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{DockerfileLiteral: ptr.To("FROM registry.svc.ci.openshift.org/org/repo:tag")}}},
			},
			expectWrite: true,
		},
		{
			name: "Existing base_image is not overwritten",
			config: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BaseImages: map[string]api.ImageStreamTagReference{
						"org_repo_tag": {Namespace: "other_org", Name: "other_repo", Tag: "other_tag"},
					},
				},
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{}},
			},
			files:       map[string][]byte{"Dockerfile": []byte("FROM registry.svc.ci.openshift.org/org/repo:tag")},
			expectWrite: true,
		},
		{
			name: "ContextDir is respected",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{ContextDir: "my-dir"}}},
			},
			files:       map[string][]byte{"my-dir/Dockerfile": []byte("FROM registry.svc.ci.openshift.org/org/repo:tag")},
			expectWrite: true,
		},
		{
			name: "Existing replace is respected",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{"some-image": {As: []string{"registry.svc.ci.openshift.org/org/repo:tag"}}}}},
				},
			},
			files: map[string][]byte{"Dockerfile": []byte("FROM registry.svc.ci.openshift.org/org/repo:tag")},
		},
		{
			name: "Replaces with tag",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "dockerfile",
					},
				}},
			},
			files:       map[string][]byte{"dockerfile": []byte("FROM registry.svc.ci.openshift.org/org/repo:tag")},
			expectWrite: true,
		},
		{
			name: "Replaces without tag",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "dockerfile",
					},
				}},
			},
			files:       map[string][]byte{"dockerfile": []byte("FROM registry.svc.ci.openshift.org/org/repo")},
			expectWrite: true,
		},
		{
			name: "Replaces Copy --from",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "dockerfile",
					},
				}},
			},
			files:       map[string][]byte{"dockerfile": []byte("COPY --from=registry.svc.ci.openshift.org/org/repo")},
			expectWrite: true,
		},
		{
			name: "Different registry, does nothing",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "dockerfile",
					},
				}},
			},
			files: map[string][]byte{"dockerfile": []byte("FROM registry.svc2.ci.openshift.org/org/repo")},
		},
		{
			name: "Build APIs replacement is executed first",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					From: "base",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "dockerfile",
					},
				}},
			},
			files:       map[string][]byte{"dockerfile": []byte("FROM registry.svc.ci.openshift.org/org/repo as repo\nFROM registry.svc.ci.openshift.org/org/repo2")},
			expectWrite: true,
		},
		{
			name: "No pruning on empty Dockerfile",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					From: "base",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "dockerfile",
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"builder"}},
						},
					},
				}},
			},
			pruneUnusedReplacementsEnabled: true,
		},
		{
			name: "OCP builder pruning happens",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
				}},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{Namespace: "ocp", Tag: "4.8"}},
				},
			},
			pruneOCPBuilderReplacementsEnabled: true,
			expectWrite:                        true,
		},
		{
			name: "OCP builder pruning skips when ocp promotion is disabled",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
				}},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{Namespace: "ocp", Tag: "4.8", Disabled: true}},
				},
			},
			pruneOCPBuilderReplacementsEnabled: true,
			expectWrite:                        false,
		},
		{
			name: "OCP builder pruning skips config which does not promote to ocp",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
				}},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{Namespace: "notocp", Tag: "4.8"}},
				},
			},
			pruneOCPBuilderReplacementsEnabled: true,
			expectWrite:                        false,
		},
		{
			name: "Dockerfile gets fixed up",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
					To: "promotionTarget",
				}},
				PromotionConfiguration: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ocp", Name: majorMinor.String()}}},
				Metadata:               api.Metadata{Branch: "master"},
			},
			ensureCorrectPromotionDockerfile:   true,
			promotionTargetToDockerfileMapping: map[string]dockerfileLocation{fmt.Sprintf("registry.ci.openshift.org/ocp/%s:promotionTarget", majorMinor.String()): {dockerfile: "Dockerfile.rhel"}},
			expectWrite:                        true,
		},
		{
			name: "Config for non-master branch is ignored",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
					To: "promotionTarget",
				}},
				PromotionConfiguration: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ocp", Name: majorMinor.String()}}}},
			ensureCorrectPromotionDockerfile:   true,
			promotionTargetToDockerfileMapping: map[string]dockerfileLocation{fmt.Sprintf("registry.svc.ci.openshift.org/ocp/%s:promotionTarget", majorMinor.String()): {dockerfile: "Dockerfile.rhel"}},
		},
		{
			name: "Dockerfile is correct, nothing to do",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "Dockerfile.rhel",
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
					To: "promotionTarget",
				}},
				PromotionConfiguration: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ocp", Name: majorMinor.String()}}},
				Metadata:               api.Metadata{Branch: "master"},
			},
			ensureCorrectPromotionDockerfile:   true,
			promotionTargetToDockerfileMapping: map[string]dockerfileLocation{fmt.Sprintf("registry.svc.ci.openshift.org/ocp/%s:promotionTarget", majorMinor.String()): {dockerfile: "Dockerfile.rhel"}},
		},
		{
			name: "Context dir gets fixed up",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "some-dir",
						DockerfilePath: "Dockerfile.rhel",
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
					To: "promotionTarget",
				}},
				PromotionConfiguration: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ocp", Name: majorMinor.String()}}},
				Metadata:               api.Metadata{Branch: "master"},
			},
			ensureCorrectPromotionDockerfile:   true,
			promotionTargetToDockerfileMapping: map[string]dockerfileLocation{fmt.Sprintf("registry.ci.openshift.org/ocp/%s:promotionTarget", majorMinor.String()): {contextDir: "other_dir", dockerfile: "Dockerfile.rhel"}},
			expectWrite:                        true,
		},
		{
			name: "Context dir is ncorrect, but ignored",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "some-dir",
						DockerfilePath: "Dockerfile.rhel",
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
					To: "promotionTarget",
				}},
				PromotionConfiguration: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ocp", Name: majorMinor.String()}}},
				Metadata:               api.Metadata{Branch: "master", Org: "org", Repo: "repo"},
			},
			ensureCorrectPromotionDockerfile:             true,
			ensureCorrectPromotionDockerfileIngoredRepos: sets.New("org/repo"),
			promotionTargetToDockerfileMapping:           map[string]dockerfileLocation{fmt.Sprintf("registry.svc.ci.openshift.org/ocp/%s:promotionTarget", majorMinor.String()): {contextDir: "other_dir", dockerfile: "Dockerfile.rhel"}},
		},
		{
			name: "Dockerfile+Context dir is correct, nothing to do",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "some_dir",
						DockerfilePath: "Dockerfile.rhel",
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
					To: "promotionTarget",
				}},
				PromotionConfiguration: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ocp", Name: majorMinor.String()}}},
				Metadata:               api.Metadata{Branch: "master"},
			},
			ensureCorrectPromotionDockerfile:   true,
			promotionTargetToDockerfileMapping: map[string]dockerfileLocation{fmt.Sprintf("registry.svc.ci.openshift.org/ocp/%s:promotionTarget", majorMinor.String()): {contextDir: "some_dir", dockerfile: "Dockerfile.rhel"}},
		},
		{
			name: "Username+Password get passed on",
			config: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "some_dir",
						DockerfilePath: "Dockerfile.rhel",
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:something"}},
						},
					},
					To: "promotionTarget",
				}},
				PromotionConfiguration: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ocp", Name: majorMinor.String()}}},
				Metadata:               api.Metadata{Branch: "master"},
			},
			ensureCorrectPromotionDockerfile:   true,
			promotionTargetToDockerfileMapping: map[string]dockerfileLocation{fmt.Sprintf("registry.svc.ci.openshift.org/ocp/%s:promotionTarget", majorMinor.String()): {contextDir: "some_dir", dockerfile: "Dockerfile.rhel"}},
			credentials:                        &usernameToken{username: "some-user", token: "some-token"},
			epectedOpts:                        github.Opts{BasicAuthUser: "some-user", BasicAuthPassword: "some-token"},
		},
		{
			name: "Unused base images are pruned",
			config: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BaseImages: map[string]api.ImageStreamTagReference{
						"old_image": {
							Name:      "old_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"other_old_image": {
							Name:      "other_old_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"fresh_image": {
							Name:      "fresh_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
					},
				},
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"image": {As: []string{"org/image"}},
						},
					},
					To: "cool_image",
				}},
				Tests: []api.TestStepConfiguration{{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Test: []api.LiteralTestStep{{
							From: "fresh_image",
						}},
					}},
				},
				Metadata: api.Metadata{Branch: "master"},
			},
			files:                        map[string][]byte{"Dockerfile": []byte("FROM registry.svc.ci.openshift.org/org/image as image")},
			pruneUnusedBaseImagesEnabled: true,
			expectWrite:                  true,
		},
		{
			name: "Used base images not pruned",
			config: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BaseImages: map[string]api.ImageStreamTagReference{
						"bundle_source_image": {
							Name:      "bundle_source_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"operator_bundle_image": {
							Name:      "operator_bundle_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"operator_substitution_image": {
							Name:      "operator_substitution_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"output_image_tag_image": {
							Name:      "output_image_tag_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"pipeline_image_cache_image": {
							Name:      "pipeline_image_cache_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"project_directory_image_build_image1": {
							Name:      "project_directory_image_build_image1",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"project_directory_image_build_image2": {
							Name:      "project_directory_image_build_image2",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"rpm_image_injection_image": {
							Name:      "rpm_image_injection_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"source_step_image": {
							Name:      "source_step_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
						"test_step_image": {
							Name:      "test_step_image",
							Namespace: "namespace",
							Tag:       "test-1.0",
						},
					},
				},
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"image": {As: []string{"registry.svc.ci.openshift.org/org/image"}},
						},
					},
					To: "cool_image",
				}},
				Operator: &api.OperatorStepConfiguration{
					Bundles: []api.Bundle{
						{BaseIndex: "operator_bundle_image"},
					},
					Substitutions: []api.PullSpecSubstitution{
						{With: "operator_substitution_image"},
					},
				},
				RawSteps: []api.StepConfiguration{
					{BundleSourceStepConfiguration: &api.BundleSourceStepConfiguration{
						Substitutions: []api.PullSpecSubstitution{
							{With: "bundle_source_image"},
						},
					}},
					{OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
						From: "output_image_tag_image",
					}},
					{PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From: "pipeline_image_cache_image",
					}},
					{ProjectDirectoryImageBuildInputs: &api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{"project_directory_image_build_image1": {As: []string{"cool-story"}}},
					}},
					{ProjectDirectoryImageBuildStepConfiguration: &api.ProjectDirectoryImageBuildStepConfiguration{
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							Inputs: map[string]api.ImageBuildInputs{"project_directory_image_build_image2": {As: []string{"cool-story"}}},
						},
					}},
					{RPMImageInjectionStepConfiguration: &api.RPMImageInjectionStepConfiguration{
						From: "rpm_image_injection_image",
					}},
					{SourceStepConfiguration: &api.SourceStepConfiguration{
						From: "source_step_image",
					}},
				},
				Tests: []api.TestStepConfiguration{{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Test: []api.LiteralTestStep{{
							Dependencies: []api.StepDependency{
								{Name: "pipeline:test_step_image"},
							},
						}},
					}},
				},
				Metadata: api.Metadata{Branch: "master"},
			},
			files:                        map[string][]byte{"Dockerfile": []byte("FROM registry.svc.ci.openshift.org/org/image as image")},
			pruneUnusedBaseImagesEnabled: true,
			expectWrite:                  false,
		},
		{
			name: "Repo in ignore-repos list is skipped",
			config: &api.ReleaseBuildConfiguration{
				Metadata: api.Metadata{
					Org:  "openshift",
					Repo: "cluster-api-operator",
				},
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "Dockerfile",
					},
				}},
			},
			files:       map[string][]byte{"Dockerfile": []byte("FROM registry.ci.openshift.org/ocp/4.19:base")},
			ignoreRepos: sets.New("openshift/cluster-api-operator"),
			expectWrite: false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts, fileGetter := fakeGithubFileGetterFactory(tc.files)
			fakeWriter := &fakeWriter{}
			if err := replacer(
				fileGetter,
				fakeWriter.Write,
				tc.pruneUnusedReplacementsEnabled,
				tc.pruneOCPBuilderReplacementsEnabled,
				tc.pruneUnusedBaseImagesEnabled,
				true,
				tc.ensureCorrectPromotionDockerfile,
				tc.ensureCorrectPromotionDockerfileIngoredRepos,
				tc.ignoreRepos,
				tc.promotionTargetToDockerfileMapping,
				majorMinor,
				nil,
				func(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error) {
					return *tc.config, nil
				},
			)(tc.config, &config.Info{Metadata: tc.config.Metadata}); err != nil {
				t.Errorf("replacer failed: %v", err)
			}
			if (fakeWriter.data != nil) != tc.expectWrite {
				t.Fatalf("expected write: %t, got data: %s", tc.expectWrite, string(fakeWriter.data))
			}

			if !tc.expectWrite {
				return
			}

			if diff := cmp.Diff(*opts, tc.epectedOpts); diff != "" {
				t.Errorf("opts differ from expected opts: %s", diff)
			}
			testhelper.CompareWithFixture(t, fakeWriter.data)
		})
	}
}

type fakeWriter struct {
	data []byte
}

func (fw *fakeWriter) Write(data []byte) error {
	fw.data = data
	return nil
}

func fakeGithubFileGetterFactory(data map[string][]byte) (*github.Opts, func(string, string, string, ...github.Opt) github.FileGetter) {
	o := &github.Opts{}
	return o, func(_, _, _ string, opts ...github.Opt) github.FileGetter {
		for _, opt := range opts {
			opt(o)
		}
		return func(path string) ([]byte, error) {
			return data[path], nil
		}
	}
}

func TestExtractReplacementCandidatesFromDockerfile(t *testing.T) {
	testCases := []struct {
		name           string
		in             string
		expectedResult sets.Set[string]
	}{
		{
			name:           "Simple",
			in:             "FROM capetown/center:1",
			expectedResult: sets.New[string]("capetown/center:1"),
		},
		{
			name:           "Copy --from",
			in:             "FROM centos:7\nCOPY --from=builder /go/src/github.com/code-ready/crc /opt/crc",
			expectedResult: sets.New[string]("centos:7", "builder"),
		},
		{
			name: "Multiple from and copy --from",
			in: `FROM registry.svc.ci.openshift.org/openshift/release:golang-1.13 AS builder
WORKDIR /go/src/github.com/kubernetes-sigs/aws-ebs-csi-driver
COPY . .
RUN make

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
# Get mkfs & blkid
RUN yum update -y && \
    yum install --setopt=tsflags=nodocs -y e2fsprogs xfsprogs util-linux && \
    yum clean all && rm -rf /var/cache/yum/*
COPY --from=builder /go/src/github.com/kubernetes-sigs/aws-ebs-csi-driver/bin/aws-ebs-csi-driver /usr/bin/
ENTRYPOINT ["/usr/bin/aws-ebs-csi-driver"]`,
			expectedResult: sets.New[string]("registry.svc.ci.openshift.org/openshift/release:golang-1.13", "builder", "registry.svc.ci.openshift.org/openshift/origin-v4.0:base"),
		},
		{
			name:           "Missing image alias name",
			in:             "FROM centos:8 AS",
			expectedResult: sets.New("centos:8"),
		},
		{
			name:           "Lowercase image alias",
			in:             "FROM centos:8 as useless",
			expectedResult: sets.New("centos:8"),
		},
		{
			name: "Unrelated directives",
			in:   "RUN somestuff\n\n\n ENV var=val",
		},
		{
			name: "Defunct from",
			in:   "from\n\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := extractReplacementCandidatesFromDockerfile([]byte(tc.in))
			if err != nil {
				t.Fatalf("error: %v", err)
			}

			if !result.Equal(tc.expectedResult) {
				t.Errorf("result does not match expected, wanted: %v, got: %v", sets.List(tc.expectedResult), sets.List(result))
			}
		})
	}
}

func TestPruneUnusedReplacements(t *testing.T) {
	testCases := []struct {
		name            string
		in              *api.ReleaseBuildConfiguration
		allSourceImages sets.Set[string]
		expected        *api.ReleaseBuildConfiguration
	}{
		{
			name: "All replacements are valid",
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {As: []string{"some-image"}},
						},
					},
				}},
			},
			allSourceImages: sets.New[string]("some-image"),
			expected: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {As: []string{"some-image"}},
						},
					}},
				},
			},
		},
		{
			name: "One As gets removed",
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {As: []string{"some-image", "superfluous"}},
						},
					}},
				},
			},
			allSourceImages: sets.New[string]("some-image"),
			expected: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {As: []string{"some-image"}},
						},
					}},
				},
			},
		},
		{
			name: "One input is empty and gets removed",
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder":   {As: []string{"some-image"}},
							"architect": {As: []string{"who-needs-this"}},
						},
					}},
				},
			},
			allSourceImages: sets.New[string]("some-image"),
			expected: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {As: []string{"some-image"}},
						},
					}},
				},
			},
		},
		{
			name: "Whole image is empty and gets removed",
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {As: []string{"some-image"}},
						},
					}},
				},
			},
			expected: &api.ReleaseBuildConfiguration{},
		},
		{
			name: "Whole image is empty but has paths directives",
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {As: []string{"some-image"}, Paths: []api.ImageSourcePath{{}}},
						},
					}},
				},
			},
			expected: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {Paths: []api.ImageSourcePath{{}}},
						},
					}},
				},
			},
		},
		{
			name: "Whole image is empty but has from",
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					From: "some-where",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {As: []string{"some-image"}},
						},
					}},
				},
			},
			expected: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					From:                             "some-where",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{}},
				},
			},
		},
		{
			name: "Whole image is empty but has to",
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					To: "some-when",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"builder": {As: []string{"some-image"}},
						},
					}},
				},
			},
			expected: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					To: "some-when",
				}},
			},
		},
		{
			name:            "cnc",
			allSourceImages: sets.New[string]("scratch", "centos:7", "builder"),
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					From: "base",
					To:   "snc",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "images/openshift-ci/Dockerfile",
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"builder"}},
						},
					}},
				},
			},
			expected: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					From: "base",
					To:   "snc",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "images/openshift-ci/Dockerfile",
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"builder"}},
						},
					}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := pruneUnusedReplacements(tc.in, tc.allSourceImages); err != nil {
				t.Fatalf("pruneUnusedReplacements failed: %v", err)
			}
			if diff := cmp.Diff(tc.in, tc.expected, cmpopts.EquateEmpty(), cmpopts.IgnoreUnexported(api.ProjectDirectoryImageBuildStepConfiguration{})); diff != "" {
				t.Errorf("result differs from expected: %s", diff)
			}
		})
	}
}

func TestPruneOCPBuilderReplacements(t *testing.T) {
	testCases := []struct {
		name     string
		in       *api.ReleaseBuildConfiguration
		expected *api.ReleaseBuildConfiguration
	}{
		{
			name: "Non-OCP builder replacement is left",
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"builder"}},
						},
					}},
				},
			},
			expected: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"builder"}},
						},
					}},
				},
			},
		},
		{
			name: "OCP builder replacement is removed",
			in: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"root": {As: []string{"ocp/builder:blub"}},
						},
					}},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "ocp",
						Tag:       "4.8",
					}},
				},
			},
			expected: &api.ReleaseBuildConfiguration{
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "ocp",
						Tag:       "4.8",
					}},
				},
			},
		},
		{
			name: "OCP builder that directly references api.ci is left",
			in: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BaseImages: map[string]api.ImageStreamTagReference{"ocp_builder_go-1.13": {
						Namespace: "ocp",
						Name:      "builder",
						Tag:       "go-1.13",
					}},
				},
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"ocp_builder_go-1.13": {As: []string{"registry.svc.ci.openshift.org/ocp/builder:go-1.13"}},
						},
					}},
				},
			},
			expected: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BaseImages: map[string]api.ImageStreamTagReference{"ocp_builder_go-1.13": {
						Namespace: "ocp",
						Name:      "builder",
						Tag:       "go-1.13",
					}},
				},
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"ocp_builder_go-1.13": {As: []string{"registry.svc.ci.openshift.org/ocp/builder:go-1.13"}},
						},
					}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := pruneOCPBuilderReplacements(tc.in); err != nil {
				t.Fatalf("pruning failed: %v", err)
			}

			if diff := cmp.Diff(tc.in, tc.expected, cmpopts.IgnoreUnexported(api.ProjectDirectoryImageBuildStepConfiguration{})); diff != "" {
				t.Errorf("actual differs from expected: %s", diff)
			}
		})
	}
}

func TestRegistryRegex(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected string
	}{
		{
			name: "some line",
			line: "some line",
		},
		{
			name:     "api.ci registry",
			line:     "FROM registry.svc.ci.openshift.org/ocp/builder:rhel-8-base-openshift-4.7",
			expected: "registry.svc.ci.openshift.org/ocp/builder:rhel-8-base-openshift-4.7",
		},
		{
			name:     "app.ci registry",
			line:     "FROM registry.ci.openshift.org/ocp/builder:rhel-8-base-openshift-4.7",
			expected: "registry.ci.openshift.org/ocp/builder:rhel-8-base-openshift-4.7",
		},
		{
			name: "need namespace",
			line: "FROM registry.ci.openshift.org/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := cidockerfile.RegistryRegex.Find([]byte(tc.line))
			if diff := cmp.Diff(tc.expected, string(actual)); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
		})
	}
}
