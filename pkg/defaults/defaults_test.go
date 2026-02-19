package defaults

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	imageapi "github.com/openshift/api/image/v1"
	templateapi "github.com/openshift/api/template/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/configresolver"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := imageapi.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func addCloneRefs(cfg *api.SourceStepConfiguration) *api.SourceStepConfiguration {
	cfg.ClonerefsPullSpec = api.ClonerefsPullSpec
	cfg.ClonerefsPath = api.ClonerefsPath
	return cfg
}

func TestStepConfigsForBuild(t *testing.T) {
	noopResolver := func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error) {
		return root, nil
	}
	var testCases = []struct {
		name          string
		input         *api.ReleaseBuildConfiguration
		jobSpec       *api.JobSpec
		output        []api.StepConfiguration
		readFile      readFile
		resolver      resolveRoot
		injectedTest  bool
		expectedError error
	}{
		{
			name: "minimal information provided",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}},
		},
		{
			name: "minimal information provided with build cache in use",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{Tag: "manual"},
						UseBuildCache:           true,
					},
				},
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error) {
				return cache, nil
			},
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "build-cache",
							Name:      "org-repo",
							Tag:       "branch",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}},
		},
		{
			name: "minimal information provided with build_root_image from repo",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						FromRepository: true,
					},
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "stream-namespace",
							Name:      "stream-name",
							Tag:       "stream-tag",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}},
			readFile: func(filename string) ([]byte, error) {
				if filename != "./.ci-operator.yaml" {
					return nil, fmt.Errorf("expected '.ci-operator.yaml' as file for the build_root_image, got %s", filename)
				}
				return []byte(`build_root_image:
  namespace: stream-namespace
  name: stream-name
  tag: stream-tag`), nil
			},
		},
		{
			name: "build_root_image from repo + build cache",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						FromRepository: true,
						UseBuildCache:  true,
					},
				},
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error) {
				return cache, nil
			},
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "build-cache",
							Name:      "org-repo",
							Tag:       "branch",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}},
			readFile: func(filename string) ([]byte, error) {
				if filename != "./.ci-operator.yaml" {
					return nil, fmt.Errorf("expected '.ci-operator.yaml' as file for the build_root_image, got %s", filename)
				}
				return []byte(`build_root_image:
  namespace: stream-namespace
  name: stream-name
  tag: stream-tag`), nil
			},
		},
		{
			name: "binary build requested",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
				},
				BinaryBuildCommands: "hi",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceSource,
					To:       api.PipelineImageStreamTagReferenceBinaries,
					Commands: "hi",
				},
			}},
		},
		{
			name: "binary and rpm build requested",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
				},
				BinaryBuildCommands: "hi",
				RpmBuildCommands:    "hello",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceSource,
					To:       api.PipelineImageStreamTagReferenceBinaries,
					Commands: "hi",
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceBinaries,
					To:       api.PipelineImageStreamTagReferenceRPMs,
					Commands: "hello; ln -s $( pwd )/_output/local/releases/rpms/ /srv/repo",
				},
			}, {
				RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRPMs,
				},
			}},
		},
		{
			name: "rpm but not binary build requested",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
				},
				RpmBuildCommands: "hello",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceSource,
					To:       api.PipelineImageStreamTagReferenceRPMs,
					Commands: "hello; ln -s $( pwd )/_output/local/releases/rpms/ /srv/repo",
				},
			}, {
				RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRPMs,
				},
			}},
		},
		{
			name: "rpm with custom output but not binary build requested",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
				},
				RpmBuildLocation: "testing",
				RpmBuildCommands: "hello",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceSource,
					To:       api.PipelineImageStreamTagReferenceRPMs,
					Commands: "hello; ln -s $( pwd )/testing /srv/repo",
				},
			}, {
				RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRPMs,
				},
			}},
		},
		{
			name: "explicit base image requested",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
					BaseImages: map[string]api.ImageStreamTagReference{
						"name": {
							Namespace: "namespace",
							Name:      "name",
							Tag:       "tag",
						},
					},
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "namespace",
							Name:      "name",
							Tag:       "tag",
							As:        "name",
						},
						To: api.PipelineImageStreamTagReference("name"),
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceBase, Name: "name"}},
				},
			}},
		},
		{
			name: "implicit base image from release configuration",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
					ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
						Namespace: "test",
						Name:      "other",
					},
					BaseImages: map[string]api.ImageStreamTagReference{
						"name": {
							Tag: "tag",
						},
					},
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{
				{
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Namespace: "root-ns",
								Name:      "root-name",
								Tag:       "manual",
							},
							To: api.PipelineImageStreamTagReferenceRoot,
						},
						Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
					},
				},
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReferenceRoot,
						To:   api.PipelineImageStreamTagReferenceSource,
					}),
				},
				{
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Namespace: "test",
								Name:      "other",
								Tag:       "tag",
								As:        "name",
							},
							To: api.PipelineImageStreamTagReference("name"),
						},
						Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceBase, Name: "name"}},
					},
				},
				{
					ReleaseImagesTagStepConfiguration: &api.ReleaseTagConfiguration{
						Namespace: "test",
						Name:      "other",
					},
				},
			},
		},
		{
			name: "rpm base image requested",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
					BaseRPMImages: map[string]api.ImageStreamTagReference{
						"name": {
							Namespace: "namespace",
							Name:      "name",
							Tag:       "tag",
						},
					},
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "namespace",
							Name:      "name",
							Tag:       "tag",
							As:        "name",
						},
						To: api.PipelineImageStreamTagReference("name-without-rpms"),
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceBaseRpm, Name: "name"}},
				},
			}, {
				RPMImageInjectionStepConfiguration: &api.RPMImageInjectionStepConfiguration{
					From: api.PipelineImageStreamTagReference("name-without-rpms"),
					To:   api.PipelineImageStreamTagReference("name"),
				},
			}},
		},
		{
			name: "including an operator bundle creates the bundle-sub and the index-gen and index images",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
				},
				Operator: &api.OperatorStepConfiguration{
					Bundles: []api.Bundle{{
						ContextDir:     "manifests/olm",
						DockerfilePath: "bundle.Dockerfile",
					}},
					Substitutions: []api.PullSpecSubstitution{{
						PullSpec: "quay.io/origin/oc",
						With:     "pipeline:oc",
					}},
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				BundleSourceStepConfiguration: &api.BundleSourceStepConfiguration{
					Substitutions: []api.PullSpecSubstitution{{
						PullSpec: "quay.io/origin/oc",
						With:     "pipeline:oc",
					}},
				},
			}, {
				IndexGeneratorStepConfiguration: &api.IndexGeneratorStepConfiguration{
					To:            "ci-index-gen",
					OperatorIndex: []string{"ci-bundle0"},
					UpdateGraph:   api.IndexUpdateSemver,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}, {
				ProjectDirectoryImageBuildStepConfiguration: (&api.ProjectDirectoryImageBuildStepConfiguration{
					To:                               "ci-index",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{DockerfilePath: "index.Dockerfile"},
				}).WithBundleImage(true),
			}, {
				ProjectDirectoryImageBuildStepConfiguration: (&api.ProjectDirectoryImageBuildStepConfiguration{
					To: "ci-bundle0",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "manifests/olm",
						DockerfilePath: "bundle.Dockerfile",
					},
				}).WithBundleImage(true),
			}, {
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: "root",
					To:   "src",
				}),
			}},
		},
		{
			name: "including an named operator bundle creates the bundle-sub and the named index-gen and index images",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
				},
				Operator: &api.OperatorStepConfiguration{
					Bundles: []api.Bundle{{
						As:             "my-bundle",
						ContextDir:     "manifests/olm",
						DockerfilePath: "bundle.Dockerfile",
					}},
					Substitutions: []api.PullSpecSubstitution{{
						PullSpec: "quay.io/origin/oc",
						With:     "pipeline:oc",
					}},
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				BundleSourceStepConfiguration: &api.BundleSourceStepConfiguration{
					Substitutions: []api.PullSpecSubstitution{{
						PullSpec: "quay.io/origin/oc",
						With:     "pipeline:oc",
					}},
				},
			}, {
				IndexGeneratorStepConfiguration: &api.IndexGeneratorStepConfiguration{
					To:            "ci-index-my-bundle-gen",
					OperatorIndex: []string{"my-bundle"},
					UpdateGraph:   api.IndexUpdateSemver,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}, {
				ProjectDirectoryImageBuildStepConfiguration: (&api.ProjectDirectoryImageBuildStepConfiguration{
					To: "ci-index-my-bundle",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "index.Dockerfile",
					},
				}).WithBundleImage(true),
			}, {
				ProjectDirectoryImageBuildStepConfiguration: (&api.ProjectDirectoryImageBuildStepConfiguration{
					To: "my-bundle",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "manifests/olm",
						DockerfilePath: "bundle.Dockerfile",
					},
				}).WithBundleImage(true),
			}, {
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: "root",
					To:   "src",
				}),
			}},
		},
		{
			name: "reading build root from repository leads to an error",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						FromRepository: true,
					},
				},
			},
			readFile: func(filename string) ([]byte, error) {
				return nil, fmt.Errorf("fail to read file: reason")
			},

			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{{
				SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRoot,
					To:   api.PipelineImageStreamTagReferenceSource,
				}),
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{

					InputImage: api.InputImage{
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
					},
					Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
				},
			}},
			expectedError: fmt.Errorf("failed to read buildRootImageStream from repository: %w", fmt.Errorf("failed to read .ci-operator.yaml file: %w", fmt.Errorf("fail to read file: reason"))),
		},
		{
			name: "from a primary ref with an additional ref",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{
						ImageStreamTagReference: &api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
					},
					BuildRootImages: map[string]api.BuildRootImageConfiguration{
						"org.other-repo": {
							ImageStreamTagReference: &api.ImageStreamTagReference{Tag: "manual"},
							UseBuildCache:           true,
						},
					},
				},
				BinaryBuildCommands: "binbuild",
				BinaryBuildCommandsList: []api.RefCommands{
					{
						Commands: "build",
						Ref:      "org.other-repo",
					},
				},
				TestBinaryBuildCommands: "build test-bin",
				TestBinaryBuildCommandsList: []api.RefCommands{
					{
						Commands: "build tb",
						Ref:      "org.other-repo",
					},
				},
				RpmBuildCommands: "build rpm",
				RpmBuildCommandsList: []api.RefCommands{
					{
						Commands: "build this-rpm",
						Ref:      "org.other-repo",
					},
				},
				RpmBuildLocation: "here",
				RpmBuildLocationList: []api.RefLocation{
					{
						Location: "there",
						Ref:      "org.other-repo",
					},
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "org",
						Repo: "repo",
					},
					ExtraRefs: []prowapi.Refs{
						{
							Org:  "org",
							Repo: "other-repo",
						},
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReferenceRoot,
						To:   api.PipelineImageStreamTagReferenceSource,
					}),
				}, {
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceRoot)),
						To:   api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceSource)),
						Ref:  "org.other-repo",
					}),
				}, {
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Namespace: "root-ns",
								Name:      "root-name",
								Tag:       "manual",
							},
							To: api.PipelineImageStreamTagReferenceRoot,
						},
						Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
					},
				}, {
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Tag: "manual",
							},
							To:  api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceRoot)),
							Ref: "org.other-repo",
						},
						Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceType(fmt.Sprintf("%s-org.other-repo", api.ImageStreamSourceRoot))}},
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReferenceSource,
						To:       api.PipelineImageStreamTagReferenceBinaries,
						Commands: "binbuild",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceSource)),
						To:       api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceBinaries)),
						Commands: "build",
						Ref:      "org.other-repo",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReferenceSource,
						To:       api.PipelineImageStreamTagReferenceTestBinaries,
						Commands: "build test-bin",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceSource)),
						To:       api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceTestBinaries)),
						Commands: "build tb",
						Ref:      "org.other-repo",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReferenceBinaries,
						To:       api.PipelineImageStreamTagReferenceRPMs,
						Commands: "build rpm; ln -s $( pwd )/here /srv/repo",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceBinaries)),
						To:       api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceRPMs)),
						Commands: "build this-rpm; ln -s $( pwd )/there /srv/repo",
						Ref:      "org.other-repo",
					},
				}, {
					RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
						From: api.PipelineImageStreamTagReferenceRPMs,
					},
				}, {
					RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceRPMs)),
						Ref:  "org.other-repo",
					},
				}},
			injectedTest: true,
		},
		{
			name: "from multiple repo references",
			input: &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImages: map[string]api.BuildRootImageConfiguration{
						"org.repo": {
							ImageStreamTagReference: &api.ImageStreamTagReference{
								Namespace: "root-ns",
								Name:      "root-name",
								Tag:       "manual",
							},
						},
						"org.other-repo": {
							ImageStreamTagReference: &api.ImageStreamTagReference{Tag: "manual"},
							UseBuildCache:           true,
						},
					},
				},
				BinaryBuildCommandsList: []api.RefCommands{
					{
						Commands: "binbuild",
						Ref:      "org.repo",
					},
					{
						Commands: "build",
						Ref:      "org.other-repo",
					},
				},
				TestBinaryBuildCommandsList: []api.RefCommands{
					{
						Commands: "build test-bin",
						Ref:      "org.repo",
					},
					{
						Commands: "build tb",
						Ref:      "org.other-repo",
					},
				},
				RpmBuildCommandsList: []api.RefCommands{
					{
						Commands: "build rpm",
						Ref:      "org.repo",
					},
					{
						Commands: "build this-rpm",
						Ref:      "org.other-repo",
					},
				},
				RpmBuildLocationList: []api.RefLocation{
					{
						Location: "here",
						Ref:      "org.repo",
					},
					{
						Location: "there",
						Ref:      "org.other-repo",
					},
				},
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					ExtraRefs: []prowapi.Refs{
						{
							Org:  "org",
							Repo: "repo",
						},
						{
							Org:  "org",
							Repo: "other-repo",
						},
					},
				},
			},
			resolver: noopResolver,
			output: []api.StepConfiguration{
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceRoot)),
						To:   api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceSource)),
						Ref:  "org.repo",
					}),
				}, {
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceRoot)),
						To:   api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceSource)),
						Ref:  "org.other-repo",
					}),
				}, {
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Namespace: "root-ns",
								Name:      "root-name",
								Tag:       "manual",
							},
							To:  api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceRoot)),
							Ref: "org.repo",
						},
						Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceType(fmt.Sprintf("%s-org.repo", api.ImageStreamSourceRoot))}},
					},
				}, {
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Tag: "manual",
							},
							To:  api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceRoot)),
							Ref: "org.other-repo",
						},
						Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceType(fmt.Sprintf("%s-org.other-repo", api.ImageStreamSourceRoot))}},
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceSource)),
						To:       api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceBinaries)),
						Commands: "binbuild",
						Ref:      "org.repo",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceSource)),
						To:       api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceBinaries)),
						Commands: "build",
						Ref:      "org.other-repo",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceSource)),
						To:       api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceTestBinaries)),
						Commands: "build test-bin",
						Ref:      "org.repo",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceSource)),
						To:       api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceTestBinaries)),
						Commands: "build tb",
						Ref:      "org.other-repo",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceBinaries)),
						To:       api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceRPMs)),
						Commands: "build rpm; ln -s $( pwd )/here /srv/repo",
						Ref:      "org.repo",
					},
				}, {
					PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
						From:     api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceBinaries)),
						To:       api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceRPMs)),
						Commands: "build this-rpm; ln -s $( pwd )/there /srv/repo",
						Ref:      "org.other-repo",
					},
				}, {
					RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.repo", api.PipelineImageStreamTagReferenceRPMs)),
						Ref:  "org.repo",
					},
				}, {
					RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-org.other-repo", api.PipelineImageStreamTagReferenceRPMs)),
						Ref:  "org.other-repo",
					},
				}},
			injectedTest: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			graphConf := FromConfigStatic(testCase.input)
			runtimeSteps, actualError := runtimeStepConfigsForBuild(testCase.input, testCase.jobSpec, testCase.readFile, testCase.resolver, graphConf.InputImages(), testCase.injectedTest)
			graphConf.Steps = append(graphConf.Steps, runtimeSteps...)
			if diff := cmp.Diff(testCase.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
			if testCase.expectedError != nil {
				return
			}
			actual := sortStepConfig(graphConf.Steps)
			expected := sortStepConfig(testCase.output)
			if diff := cmp.Diff(actual, expected, cmp.AllowUnexported(api.ProjectDirectoryImageBuildStepConfiguration{})); diff != "" {
				t.Errorf("actual differs from expected: %s", diff)
			}
		})
	}
}

func sortStepConfig(in []api.StepConfiguration) []api.StepConfiguration {
	sort.Slice(in, func(i, j int) bool {
		iMarshalled, err := json.Marshal(in[i])
		if err != nil {
			panic(fmt.Sprintf("iMarshal: %v", err))
		}
		jMarshalled, err := json.Marshal(in[j])
		if err != nil {
			panic(fmt.Sprintf("jMarshal: %v", err))
		}
		return string(iMarshalled) < string(jMarshalled)
	})
	return in
}

type environmentOverride struct {
	m map[string]string
}

func (e environmentOverride) Has(name string) bool {
	_, ok := e.m[name]
	return ok
}

func (e environmentOverride) HasInput(name string) bool {
	return e.Has(name)
}

func (e environmentOverride) Get(name string) (string, error) {
	return e.m[name], nil
}

func TestFromConfig(t *testing.T) {
	ns := "ns"
	httpClient := release.NewFakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		content := `{"nodes": [{"version": "4.1.0", "payload": "payload"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(content))),
		}, nil
	})
	client := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().Build(), nil)
	if err := imageapi.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	for _, i := range []struct {
		name string
		tags []string
	}{{
		name: "pipeline",
		tags: []string{
			"base_image", "base_rpm_image-without-rpms", "rpms",
			"base_rpm_image-org.repo1-without-rpms", "base_rpm_image-org.repo2-without-rpms",
			"rpms-org.repo1", "rpms-org.repo2",
			"src", "bin", "to",
			"ci-bundle0", "ci-index",
			"machine-os-content",
			"tool1", "tool2", "tool3",
		},
	}, {
		name: "release",
		tags: []string{"initial", "latest", "release"},
	}, {
		name: "from",
		tags: []string{"latest"},
	}} {
		var tags []imageapi.NamedTagEventList
		for _, t := range i.tags {
			tags = append(tags, imageapi.NamedTagEventList{
				Tag: t,
				Items: []imageapi.TagEvent{
					{DockerImageReference: "docker_image_reference"},
				},
			})
		}
		err := client.Create(context.Background(), &imageapi.ImageStream{
			ObjectMeta: meta.ObjectMeta{Name: i.name, Namespace: ns},
			Status: imageapi.ImageStreamStatus{
				PublicDockerImageRepository: "public_docker_image_repository",
				Tags:                        tags,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	buildClient := steps.NewBuildClient(client, nil, nil, "", "", nil)
	var templateClient steps.TemplateClient
	podClient := kubernetes.NewPodClient(client, nil, nil, 0, nil)

	clusterPool := hivev1.ClusterPool{
		ObjectMeta: meta.ObjectMeta{
			Name:      "pool1",
			Namespace: "ci-cluster-pool",
			Labels: map[string]string{
				"product":      string(api.ReleaseProductOCP),
				"version":      "4.7",
				"architecture": string(api.ReleaseArchitectureAMD64),
				"cloud":        string(api.CloudAWS),
				"owner":        "dpp",
			},
		},
		Spec: hivev1.ClusterPoolSpec{
			ImageSetRef: hivev1.ClusterImageSetReference{
				Name: "ocp-4.7.0-amd64",
			},
		},
	}
	imageset := hivev1.ClusterImageSet{
		ObjectMeta: meta.ObjectMeta{
			Name: "ocp-4.7.0-amd64",
		},
		Spec: hivev1.ClusterImageSetSpec{
			ReleaseImage: "pullspec",
		},
	}
	scheme := scheme.Scheme
	if err := hivev1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hive scheme to runtime schema: %v", err)
	}
	hiveClient := fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme).WithObjects(&clusterPool, &imageset).Build()

	var leaseClient *lease.Client
	var cloneAuthConfig *steps.CloneAuthConfig
	pullSecret, pushSecret := &coreapi.Secret{}, &coreapi.Secret{}
	for _, tc := range []struct {
		name                string
		config              api.ReleaseBuildConfiguration
		refs                *prowapi.Refs
		paramFiles          string
		promote             bool
		templates           []*templateapi.Template
		env                 api.Parameters
		params              map[string]string
		overriddenImagesEnv map[string]string
		injectedTest        bool
		requiredTargets     []string
		skippedImages       sets.Set[string]
		enableLeaseClient   bool
		expectedSteps       []string
		expectedPost        []string
		expectedParams      map[string]string
		expectedErr         error
	}{{
		name:          "no steps",
		expectedSteps: []string{"[output-images]", "[images]"},
	}, {
		name: "input image",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseImages: map[string]api.ImageStreamTagReference{
					"base_image": {Name: "name", Namespace: "ns", Tag: "tag"},
				},
			},
		},
		expectedSteps: []string{
			"[input:base_image]",
			"[output-images]",
			"[images]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_BASE_IMAGE": "public_docker_image_repository:base_image",
		},
	}, {
		name:          "source build",
		refs:          &prowapi.Refs{Org: "org", Repo: "repo"},
		expectedSteps: []string{"src", "[output-images]", "[images]"},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_SRC": "public_docker_image_repository:src",
		},
	}, {
		name: "bundle source",
		config: api.ReleaseBuildConfiguration{
			Operator: &api.OperatorStepConfiguration{
				Bundles: []api.Bundle{{
					DockerfilePath: "dockerfile_path",
					ContextDir:     "context_dir",
				}},
			},
		},
		expectedSteps: []string{
			"src-bundle",
			"ci-bundle0",
			"ci-index-gen",
			"ci-index",
			"[output-images]",
			"[images]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_CI_BUNDLE0": "public_docker_image_repository:ci-bundle0",
			"LOCAL_IMAGE_CI_INDEX":   "public_docker_image_repository:ci-index",
		},
	}, {
		name: "image build",
		config: api.ReleaseBuildConfiguration{
			Images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{From: "from", To: "to"},
			},
		},
		expectedSteps: []string{
			"to",
			"[output:stable:to]",
			"[output-images]",
			"[images]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_TO": "public_docker_image_repository:to",
		},
	}, {
		name: "build root",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BuildRootImage: &api.BuildRootImageConfiguration{
					ProjectImageBuild: &api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "context_dir",
						DockerfilePath: "dockerfile_path",
					},
				},
			},
		},
		expectedSteps: []string{"root", "[output-images]", "[images]"},
	}, {
		name: "multiple build roots",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BuildRootImages: map[string]api.BuildRootImageConfiguration{
					"org.repo1": {
						ProjectImageBuild: &api.ProjectDirectoryImageBuildInputs{
							ContextDir:     "context_dir",
							DockerfilePath: "dockerfile_path",
						}},
					"org.repo2": {ProjectImageBuild: &api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "context_dir",
						DockerfilePath: "dockerfile_path",
					}},
				},
			},
		},
		injectedTest:  true,
		expectedSteps: []string{"root-org.repo1", "root-org.repo2", "[output-images]", "[images]"},
	}, {
		name: "base RPM images",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseRPMImages: map[string]api.ImageStreamTagReference{
					"base_rpm_image": {
						Name:      "base_rpm_image",
						Namespace: ns,
						Tag:       "tag",
					},
				},
			},
		},
		expectedSteps: []string{
			"[input:base_rpm_image-without-rpms]",
			"base_rpm_image",
			"[output-images]",
			"[images]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_BASE_RPM_IMAGE_WITHOUT_RPMS": "public_docker_image_repository:base_rpm_image-without-rpms",
		},
	}, {
		name: "multiple base RPM images",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseRPMImages: map[string]api.ImageStreamTagReference{
					"base_rpm_image-org.repo1": {
						Name:      "base_rpm_image",
						Namespace: ns,
						Tag:       "tag",
					},
					"base_rpm_image-org.repo2": {
						Name:      "base_rpm_image",
						Namespace: ns,
						Tag:       "tag",
					},
				},
			},
		},
		injectedTest: true,
		expectedSteps: []string{
			"[input:base_rpm_image-org.repo1-without-rpms]",
			"base_rpm_image-org.repo1",
			"[input:base_rpm_image-org.repo2-without-rpms]",
			"base_rpm_image-org.repo2",
			"[output-images]",
			"[images]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_BASE_RPM_IMAGE_ORG.REPO1_WITHOUT_RPMS": "public_docker_image_repository:base_rpm_image-org.repo1-without-rpms",
			"LOCAL_IMAGE_BASE_RPM_IMAGE_ORG.REPO2_WITHOUT_RPMS": "public_docker_image_repository:base_rpm_image-org.repo2-without-rpms",
		},
	}, {
		name: "RPM build",
		config: api.ReleaseBuildConfiguration{
			RpmBuildCommands: "make rpm",
		},
		expectedSteps: []string{
			"rpms",
			"[serve:rpms]",
			"[output-images]",
			"[images]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_RPMS": "public_docker_image_repository:rpms",
		},
	}, {
		name: "multiple RPM builds",
		config: api.ReleaseBuildConfiguration{
			RpmBuildCommandsList: []api.RefCommands{
				{
					Ref:      "org.repo1",
					Commands: "make rpm",
				},
				{
					Ref:      "org.repo2",
					Commands: "make other-rpm",
				},
			},
		},
		injectedTest: true,
		expectedSteps: []string{
			"rpms-org.repo1",
			"[serve:rpms-org.repo1]",
			"rpms-org.repo2",
			"[serve:rpms-org.repo2]",
			"[output-images]",
			"[images]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_RPMS_ORG.REPO1": "public_docker_image_repository:rpms-org.repo1",
			"LOCAL_IMAGE_RPMS_ORG.REPO2": "public_docker_image_repository:rpms-org.repo2",
		},
	}, {
		name: "tag specification",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
					Name:      "tag_specification",
					Namespace: ns,
				},
			},
		},
		expectedSteps: []string{
			"[release:initial]",
			"[release:latest]",
			"[release-inputs]",
			"[images]",
		},
		expectedParams: map[string]string{
			"IMAGE_FORMAT":          "public_docker_image_repository/ns/stable:${component}",
			"RELEASE_IMAGE_INITIAL": "public_docker_image_repository:initial",
			"RELEASE_IMAGE_LATEST":  "public_docker_image_repository:latest",
		},
	}, {
		name: "tag specification with input",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
					Name:      "tag_specification",
					Namespace: ns,
				},
			},
		},
		env: environmentOverride{
			m: map[string]string{
				utils.ReleaseImageEnv(api.LatestReleaseName): "latest",
			},
		},
		expectedSteps: []string{
			"[release:initial]",
			"[release:latest]",
			"[release-inputs]",
			"[images]",
		},
		expectedParams: map[string]string{
			"IMAGE_FORMAT":                  "public_docker_image_repository/ns/stable:${component}",
			"RELEASE_IMAGE_INITIAL":         "public_docker_image_repository:initial",
			"RELEASE_IMAGE_LATEST":          "public_docker_image_repository:latest",
			"ORIGINAL_RELEASE_IMAGE_LATEST": "",
		},
	}, {
		name: "resolve release",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				Releases: map[string]api.UnresolvedRelease{
					"release": {Release: &api.Release{Version: "4.1.0"}},
				},
			},
		},
		expectedSteps: []string{"[release:release]", "[images]"},
		expectedParams: map[string]string{
			utils.ReleaseImageEnv("release"): "public_docker_image_repository:release",
			"ORIGINAL_RELEASE_IMAGE_RELEASE": "",
		},
	}, {
		name: "resolve release with input",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				Releases: map[string]api.UnresolvedRelease{
					"release": {Release: &api.Release{Version: "4.1.0"}},
				},
			},
		},
		env: environmentOverride{
			m: map[string]string{
				utils.ReleaseImageEnv("release"): "release",
			},
		},
		expectedSteps: []string{"[release:release]", "[images]"},
		expectedParams: map[string]string{
			utils.ReleaseImageEnv("release"): "public_docker_image_repository:release",
			"ORIGINAL_RELEASE_IMAGE_RELEASE": "",
		},
	}, {
		name: "container test",
		config: api.ReleaseBuildConfiguration{
			Tests: []api.TestStepConfiguration{{
				As:                         "test",
				ContainerTestConfiguration: &api.ContainerTestConfiguration{},
			}},
		},
		expectedSteps: []string{"test", "[output-images]", "[images]"},
	}, {
		name: "openshift-installer test",
		config: api.ReleaseBuildConfiguration{
			Tests: []api.TestStepConfiguration{{
				As: "test",
				OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{},
			}},
		},
		expectedSteps: []string{"[output-images]", "[images]"},
	}, {
		name: "openshift-installer upgrade test",
		config: api.ReleaseBuildConfiguration{
			Tests: []api.TestStepConfiguration{{
				As: "test",
				OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
					Upgrade: true,
				},
			}},
		},
		expectedSteps: []string{"test", "[output-images]", "[images]"},
	}, {
		name: "multi-stage test",
		config: api.ReleaseBuildConfiguration{
			Tests: []api.TestStepConfiguration{{
				As:                                 "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{},
			}},
		},
		expectedSteps: []string{"test", "[output-images]", "[images]"},
	}, {
		name: "multi-stage test with a cluster claim",
		config: api.ReleaseBuildConfiguration{
			Tests: []api.TestStepConfiguration{{
				As: "fast-as-heck-aws",
				ClusterClaim: &api.ClusterClaim{
					Product:      api.ReleaseProductOCP,
					Version:      "4.7",
					Architecture: api.ReleaseArchitectureAMD64,
					Cloud:        api.CloudAWS,
					Owner:        "dpp",
				},
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{},
			}},
		},
		expectedSteps: []string{
			"[release:latest-fast-as-heck-aws]",
			"fast-as-heck-aws",
			"[output-images]",
			"[images]",
		},
	}, {
		name: "container test with a claim",
		config: api.ReleaseBuildConfiguration{
			Tests: []api.TestStepConfiguration{{
				As:                         "e2e",
				ClusterClaim:               &api.ClusterClaim{},
				ContainerTestConfiguration: &api.ContainerTestConfiguration{},
			}},
		},
		expectedSteps: []string{"e2e", "[output-images]", "[images]"},
	}, {
		name: "lease test",
		config: api.ReleaseBuildConfiguration{
			Tests: []api.TestStepConfiguration{{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					ClusterProfile: api.ClusterProfileAWS,
				},
			}},
		},
		expectedSteps: []string{"test", "[output-images]", "[images]"},
	}, {
		name: "template test",
		templates: []*templateapi.Template{
			{ObjectMeta: meta.ObjectMeta{Name: "template"}},
		},
		expectedSteps: []string{"template", "[output-images]", "[images]"},
	}, {
		name: "template test with lease",
		templates: []*templateapi.Template{{
			ObjectMeta: meta.ObjectMeta{Name: "template"},
			Parameters: []templateapi.Parameter{
				{Name: "USE_LEASE_CLIENT"},
				{Name: "CLUSTER_TYPE", Required: true},
			},
		}},
		params:        map[string]string{"CLUSTER_TYPE": "aws"},
		expectedSteps: []string{"template", "[output-images]", "[images]"},
		expectedParams: map[string]string{
			"CLUSTER_TYPE":      "aws",
			api.DefaultLeaseEnv: "",
		},
	}, {
		name:       "param files",
		paramFiles: "param_files",
		expectedSteps: []string{
			"parameters/write",
			"[output-images]",
			"[images]",
		},
	}, {
		name: "promote",
		config: api.ReleaseBuildConfiguration{
			PromotionConfiguration: &api.PromotionConfiguration{
				Targets: []api.PromotionTarget{{
					Namespace: ns,
					Name:      "name",
					Tag:       "tag",
				}},
			},
		},
		promote:       true,
		expectedSteps: []string{"[output-images]", "[images]"},
		expectedPost:  []string{"[promotion]", "[promotion-quay]"},
	}, {
		name: "duplicate input images",
		config: api.ReleaseBuildConfiguration{
			Tests: []api.TestStepConfiguration{{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Test: []api.LiteralTestStep{{
						FromImage: &api.ImageStreamTagReference{
							Namespace: ns,
							Name:      "base_image",
							Tag:       "tag",
						},
					}, {
						FromImage: &api.ImageStreamTagReference{
							Namespace: ns,
							Name:      "base_image",
							Tag:       "tag",
						},
					}},
				},
			}},
		},
		expectedSteps: []string{
			"test",
			"[input:ns-base_image-tag]",
			"[output-images]",
			"[images]",
		},
	}, {
		name: "override image",
		overriddenImagesEnv: map[string]string{
			"OVERRIDE_IMAGE_MACHINE_OS_CONTENT": "4.16.2",
		},
		expectedSteps: []string{
			"[images]",
			"[input:machine-os-content]",
			"[output-images]",
			"[output:stable:machine-os-content]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_MACHINE_OS_CONTENT": "public_docker_image_repository:machine-os-content",
		},
	}, {
		name: "test step sources",
		config: api.ReleaseBuildConfiguration{
			Tests: []api.TestStepConfiguration{{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Test: []api.LiteralTestStep{{
						As: "step1",
						FromImage: &api.ImageStreamTagReference{
							Namespace: ns,
							Name:      "cool_image",
							Tag:       "tag",
						},
					}, {
						As: "step2",
						FromImage: &api.ImageStreamTagReference{
							Namespace: ns,
							Name:      "cooler_image",
							Tag:       "tag",
						},
					}, {
						As: "step3",
						FromImage: &api.ImageStreamTagReference{
							Namespace: ns,
							Name:      "cool_image",
							Tag:       "tag",
						},
					}},
				},
			}},
		},
		expectedSteps: []string{
			"test",
			"[input:ns-cool_image-tag]",
			"[input:ns-cooler_image-tag]",
			"[output-images]",
			"[images]",
		},
	}, {
		name: "image with BuildImagesIfAffected enabled but not targeted [images]",
		config: api.ReleaseBuildConfiguration{
			Images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{From: "from", To: "tool1"},
				{From: "from", To: "tool2"},
			},
			BuildImagesIfAffected: true,
		},
		expectedSteps: []string{
			"tool1",
			"[output:stable:tool1]",
			"tool2",
			"[output:stable:tool2]",
			"[output-images]",
			"[images]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_TOOL1": "public_docker_image_repository:tool1",
			"LOCAL_IMAGE_TOOL2": "public_docker_image_repository:tool2",
		},
	}, {
		name: "image with BuildImagesIfAffected enabled and targeted [images]",
		config: api.ReleaseBuildConfiguration{
			Images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{From: "from", To: "tool1"},
				{From: "from", To: "tool2"},
				{From: "from", To: "tool3"},
			},
			BuildImagesIfAffected: true,
		},
		requiredTargets: []string{"[images]"},
		skippedImages:   sets.New("tool2", "tool3"),
		expectedSteps: []string{
			"tool1",
			"[output:stable:tool1]",
			"[output-images]",
			"[images]",
		},
		expectedParams: map[string]string{
			"LOCAL_IMAGE_TOOL1": "public_docker_image_repository:tool1",
		},
	}, {
		name:              "enable lease proxy server",
		enableLeaseClient: true,
		expectedSteps:     []string{"[output-images]", "[images]", "lease-proxy-server"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			jobSpec := api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:  "job_name",
					Refs: tc.refs,
				},
				TargetAdditionalSuffix: "1",
			}
			jobSpec.SetNamespace(ns)
			for k, v := range tc.overriddenImagesEnv {
				t.Setenv(k, v)
			}
			params := api.NewDeferredParameters(tc.env)
			for k, v := range tc.params {
				params.Add(k, func() (string, error) { return v, nil })
			}
			graphConf := FromConfigStatic(&tc.config)
			cfg := &Config{
				Clients: Clients{
					LeaseClientEnabled: tc.enableLeaseClient,
					LeaseClient:        leaseClient,
					kubeClient:         client,
					buildClient:        buildClient,
					templateClient:     templateClient,
					podClient:          podClient,
					hiveClient:         hiveClient,
					httpClient:         httpClient,
				},
				CIConfig:                    &tc.config,
				GraphConf:                   &graphConf,
				JobSpec:                     &jobSpec,
				Templates:                   tc.templates,
				ParamFile:                   tc.paramFiles,
				Promote:                     tc.promote,
				RequiredTargets:             tc.requiredTargets,
				CloneAuthConfig:             cloneAuthConfig,
				PullSecret:                  pullSecret,
				PushSecret:                  pushSecret,
				params:                      params,
				Censor:                      &secrets.DynamicCensor{},
				NodeName:                    api.ServiceDomainAPPCI,
				TargetAdditionalSuffix:      "",
				NodeArchitectures:           nil,
				IntegratedStreams:           map[string]*configresolver.IntegratedStream{},
				InjectedTest:                tc.injectedTest,
				EnableSecretsStoreCSIDriver: false,
				MetricsAgent:                nil,
				SkippedImages:               tc.skippedImages,
			}
			configSteps, post, err := fromConfig(context.Background(), cfg)
			if diff := cmp.Diff(tc.expectedErr, err); diff != "" {
				t.Errorf("unexpected error: %v", diff)
			}
			var stepNames, postNames []string

			for _, s := range configSteps {
				stepNames = append(stepNames, s.Name())
			}
			for _, s := range post {
				postNames = append(postNames, s.Name())
			}
			paramMap, err := params.Map()
			if err != nil {
				t.Fatal(err)
			}
			if tc.expectedParams == nil {
				tc.expectedParams = map[string]string{}
			}
			for k, v := range map[string]string{
				"JOB_NAME":      "job_name",
				"JOB_NAME_HASH": jobSpec.JobNameHash(),
				"JOB_NAME_SAFE": "job-name",
				"UNIQUE_HASH":   jobSpec.UniqueHash(),
				"NAMESPACE":     ns,
			} {
				tc.expectedParams[k] = v
			}

			if diff := cmp.Diff(tc.expectedParams, paramMap); diff != "" {
				t.Errorf("unexpected parameters: %v", diff)
			}
			// When using multiples, there are steps where the ordering will not be deterministic so we must sort
			sort.Strings(stepNames)
			sort.Strings(tc.expectedSteps)
			if diff := cmp.Diff(tc.expectedSteps, stepNames); diff != "" {
				t.Errorf("unexpected steps: %v", diff)
			}
			if diff := cmp.Diff(tc.expectedPost, postNames); diff != "" {
				t.Errorf("unexpected post steps: %v", diff)
			}
		})
	}
}

func TestRegistryDomain(t *testing.T) {
	var testCases = []struct {
		name     string
		config   *api.PromotionConfiguration
		expected string
	}{
		{
			name:     "default",
			config:   &api.PromotionConfiguration{},
			expected: "registry.ci.openshift.org",
		},
		{
			name:     "override",
			config:   &api.PromotionConfiguration{RegistryOverride: "whoa.com.biz"},
			expected: "whoa.com.biz",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if diff := cmp.Diff(testCase.expected, registryDomain(testCase.config)); diff != "" {
				t.Errorf("%s: got incorrect registry domain: %v", testCase.name, diff)
			}
		})
	}
}

func TestGetSourceStepsForJobSpec(t *testing.T) {
	testCases := []struct {
		name         string
		jobSpec      api.JobSpec
		injectedTest bool
		expected     []api.StepConfiguration
	}{
		{
			name: "simple presubmit should include the only ref with no suffix",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type: "presubmit",
					Refs: &prowapi.Refs{Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "ABCD",
						Pulls: []prowapi.Pull{{Number: 1234, Author: "developer", SHA: "ABCDEFG", Ref: "some-branch"}},
					},
				},
			},
			expected: []api.StepConfiguration{
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReferenceRoot,
						To:   api.PipelineImageStreamTagReferenceSource,
					}),
				},
			},
		},
		{
			name: "rehearsal presubmit should include only the extra_ref with no suffix",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type: "presubmit",
					Refs: &prowapi.Refs{Org: "openshift", Repo: "release", BaseRef: "main", BaseSHA: "ABCD",
						Pulls: []prowapi.Pull{{Number: 1234, Author: "developer", SHA: "ABCDEFG", Ref: "some-branch"}},
					},
					ExtraRefs: []prowapi.Refs{{WorkDir: true, Org: "openshift", Repo: "other-repo", BaseRef: "main", BaseSHA: "ABCDEFG"}},
				},
			},
			expected: []api.StepConfiguration{
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReferenceRoot,
						To:   api.PipelineImageStreamTagReferenceSource,
					}),
				},
			},
		},
		{
			name: "simple periodic should include only the extra_ref with no suffix",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type:      "periodic",
					ExtraRefs: []prowapi.Refs{{Org: "openshift", Repo: "other-repo", BaseRef: "main", BaseSHA: "ABCDEFG"}},
				},
			},
			expected: []api.StepConfiguration{
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReferenceRoot,
						To:   api.PipelineImageStreamTagReferenceSource,
					}),
				},
			},
		},
		{
			name: "payload testing periodic should include all extra_refs with suffix",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type: "periodic",
					ExtraRefs: []prowapi.Refs{
						{Org: "openshift", Repo: "repo", BaseRef: "main", BaseSHA: "ABCDEFG"},
						{Org: "openshift", Repo: "other-repo", BaseRef: "master", BaseSHA: "GHTIKD"},
						{Org: "openshift", Repo: "repo-three", BaseRef: "master", BaseSHA: "TYIER"},
					},
				},
			},
			injectedTest: true,
			expected: []api.StepConfiguration{
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.repo", api.PipelineImageStreamTagReferenceRoot)),
						To:   api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.repo", api.PipelineImageStreamTagReferenceSource)),
						Ref:  "openshift.repo",
					}),
				},
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.other-repo", api.PipelineImageStreamTagReferenceRoot)),
						To:   api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.other-repo", api.PipelineImageStreamTagReferenceSource)),
						Ref:  "openshift.other-repo",
					}),
				},
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.repo-three", api.PipelineImageStreamTagReferenceRoot)),
						To:   api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.repo-three", api.PipelineImageStreamTagReferenceSource)),
						Ref:  "openshift.repo-three",
					}),
				},
			},
		},
		{
			name: "multi-pr presubmit testing should include primary ref without suffix, and all extra_refs with suffix",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type: "periodic",
					Refs: &prowapi.Refs{Org: "openshift", Repo: "repo", BaseRef: "main", BaseSHA: "ABCDEFG"},
					ExtraRefs: []prowapi.Refs{
						{Org: "openshift", Repo: "other-repo", BaseRef: "master", BaseSHA: "GHTIKD"},
						{Org: "openshift", Repo: "repo-three", BaseRef: "master", BaseSHA: "TYIER"},
					},
				},
			},
			injectedTest: true,
			expected: []api.StepConfiguration{
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReferenceRoot,
						To:   api.PipelineImageStreamTagReferenceSource,
					}),
				},
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.other-repo", api.PipelineImageStreamTagReferenceRoot)),
						To:   api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.other-repo", api.PipelineImageStreamTagReferenceSource)),
						Ref:  "openshift.other-repo",
					}),
				},
				{
					SourceStepConfiguration: addCloneRefs(&api.SourceStepConfiguration{
						From: api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.repo-three", api.PipelineImageStreamTagReferenceRoot)),
						To:   api.PipelineImageStreamTagReference(fmt.Sprintf("%s-openshift.repo-three", api.PipelineImageStreamTagReferenceSource)),
						Ref:  "openshift.repo-three",
					}),
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := getSourceStepsForJobSpec(&tc.jobSpec, tc.injectedTest)
			less := func(a, b api.StepConfiguration) bool {
				return a.SourceStepConfiguration.Ref < b.SourceStepConfiguration.Ref
			}
			if diff := cmp.Diff(tc.expected, result, cmpopts.SortSlices(less)); diff != "" {
				t.Errorf("%s: result didn't match expected, diff: %v", tc.name, diff)
			}
		})
	}
}

func TestDeterminePrimaryRef(t *testing.T) {
	testCases := []struct {
		name         string
		jobSpec      api.JobSpec
		injectedTest bool
		expected     *prowapi.Refs
	}{
		{
			name: "presubmit with one ref, returns that ref",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type: "presubmit",
					Refs: &prowapi.Refs{Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "ABCD",
						Pulls: []prowapi.Pull{{Number: 1234, Author: "developer", SHA: "ABCDEFG", Ref: "some-branch"}},
					},
				},
			},
			expected: &prowapi.Refs{Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "ABCD",
				Pulls: []prowapi.Pull{{Number: 1234, Author: "developer", SHA: "ABCDEFG", Ref: "some-branch"}},
			},
		},
		{
			name: "periodic with one extra_ref, returns that ref",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type:      "presubmit",
					ExtraRefs: []prowapi.Refs{{Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "ABCD"}},
				},
			},
			expected: &prowapi.Refs{Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "ABCD"},
		},
		{
			name: "rehearsal, returns the 'workdir' extra_ref",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type: "presubmit",
					Refs: &prowapi.Refs{Org: "openshift", Repo: "release", BaseRef: "main", BaseSHA: "ABCD",
						Pulls: []prowapi.Pull{{Number: 1234, Author: "developer", SHA: "ABCDEFG", Ref: "some-branch"}},
					},
					ExtraRefs: []prowapi.Refs{{WorkDir: true, Org: "openshift", Repo: "other-repo", BaseRef: "main", BaseSHA: "ABCDEFG"}},
				},
			},
			expected: &prowapi.Refs{WorkDir: true, Org: "openshift", Repo: "other-repo", BaseRef: "main", BaseSHA: "ABCDEFG"},
		},
		{
			name: "multi-pr presubmit, returns the ref not any of the extra_refs",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type: "presubmit",
					Refs: &prowapi.Refs{Org: "openshift", Repo: "repo", BaseRef: "main", BaseSHA: "ABCDEFG"},
					ExtraRefs: []prowapi.Refs{
						{Org: "openshift", Repo: "other-repo", BaseRef: "master", BaseSHA: "GHTIKD"},
						{Org: "openshift", Repo: "repo-three", BaseRef: "master", BaseSHA: "TYIER"},
					},
				},
			},
			expected: &prowapi.Refs{Org: "openshift", Repo: "repo", BaseRef: "main", BaseSHA: "ABCDEFG"},
		},
		{
			name: "payload test with multiple extra_refs, returns nil",
			jobSpec: api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Type: "periodic",
					ExtraRefs: []prowapi.Refs{
						{Org: "openshift", Repo: "repo", BaseRef: "main", BaseSHA: "ABCDEFG"},
						{Org: "openshift", Repo: "other-repo", BaseRef: "master", BaseSHA: "GHTIKD"},
						{Org: "openshift", Repo: "repo-three", BaseRef: "master", BaseSHA: "TYIER"},
					},
				},
			},
			expected: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			primaryRef := determinePrimaryRef(&tc.jobSpec, tc.injectedTest)
			if diff := cmp.Diff(tc.expected, primaryRef); diff != "" {
				t.Errorf("%s: result didn't match expected, diff: %v", tc.name, diff)
			}
		})
	}
}

func TestFilterRequiredBinariesFromSkipped(t *testing.T) {
	testCases := []struct {
		name            string
		images          []api.ProjectDirectoryImageBuildStepConfiguration
		skippedImages   sets.Set[string]
		expectedSkipped sets.Set[string]
	}{
		{
			name: "required binary is removed from skipped set",
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{
					To: "composite-tool",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"bin": {
								Paths: []api.ImageSourcePath{
									{SourcePath: "/go/bin/composite-tool"},
									{SourcePath: "/go/bin/dependency-tool"},
								},
							},
						},
					},
				},
				{To: "dependency-tool"},
				{To: "unrelated-tool"},
			},
			skippedImages:   sets.New("dependency-tool", "unrelated-tool"),
			expectedSkipped: sets.New("unrelated-tool"),
		},
		{
			name: "multiple required binaries are removed",
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{
					To: "composite-tool",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						Inputs: map[string]api.ImageBuildInputs{
							"bin": {
								Paths: []api.ImageSourcePath{
									{SourcePath: "/go/bin/dep1"},
									{SourcePath: "/go/bin/dep2"},
								},
							},
						},
					},
				},
				{To: "dep1"},
				{To: "dep2"},
				{To: "other-tool"},
			},
			skippedImages:   sets.New("dep1", "dep2", "other-tool"),
			expectedSkipped: sets.New("other-tool"),
		},
		{
			name: "no bin dependencies - all skipped remain",
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: "tool1"},
				{To: "tool2"},
				{To: "tool3"},
			},
			skippedImages:   sets.New("tool2", "tool3"),
			expectedSkipped: sets.New("tool2", "tool3"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := filterRequiredBinariesFromSkipped(tc.images, tc.skippedImages)
			if diff := cmp.Diff(tc.expectedSkipped, result); diff != "" {
				t.Errorf("skipped binaries differ:\n%s", diff)
			}
		})
	}
}

func TestReadDockerfileForImage(t *testing.T) {
	testCases := []struct {
		name            string
		image           api.ProjectDirectoryImageBuildStepConfiguration
		readFile        readFile
		expectedContent string
		expectedPath    string
		expectError     bool
	}{
		{
			name: "default Dockerfile path",
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "test-image",
			},
			readFile: func(path string) ([]byte, error) {
				if path == "./Dockerfile" {
					return []byte("FROM registry.ci.openshift.org/ocp/4.19:base"), nil
				}
				return nil, errors.New("file not found")
			},
			expectedContent: "FROM registry.ci.openshift.org/ocp/4.19:base",
			expectedPath:    "./Dockerfile",
			expectError:     false,
		},
		{
			name: "custom Dockerfile path",
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "test-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					DockerfilePath: "Dockerfile.rhel",
				},
			},
			readFile: func(path string) ([]byte, error) {
				if path == "./Dockerfile.rhel" {
					return []byte("FROM registry.ci.openshift.org/ocp/builder:rhel-9"), nil
				}
				return nil, errors.New("file not found")
			},
			expectedContent: "FROM registry.ci.openshift.org/ocp/builder:rhel-9",
			expectedPath:    "./Dockerfile.rhel",
			expectError:     false,
		},
		{
			name: "with context directory",
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "test-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					ContextDir:     "images/myimage",
					DockerfilePath: "Dockerfile.custom",
				},
			},
			readFile: func(path string) ([]byte, error) {
				if path == "images/myimage/Dockerfile.custom" {
					return []byte("FROM registry.ci.openshift.org/ocp/4.19:tools"), nil
				}
				return nil, errors.New("file not found")
			},
			expectedContent: "FROM registry.ci.openshift.org/ocp/4.19:tools",
			expectedPath:    "images/myimage/Dockerfile.custom",
			expectError:     false,
		},
		{
			name: "DockerfileLiteral",
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "test-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					DockerfileLiteral: ptr.To("FROM registry.ci.openshift.org/ocp/4.19:literal"),
				},
			},
			readFile: func(path string) ([]byte, error) {
				return nil, errors.New("should not be called")
			},
			expectedContent: "FROM registry.ci.openshift.org/ocp/4.19:literal",
			expectedPath:    "dockerfile_literal",
			expectError:     false,
		},
		{
			name: "file not found",
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "test-image",
			},
			readFile: func(path string) ([]byte, error) {
				return nil, errors.New("no such file")
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			details, err := readDockerfileForImage(tc.image, tc.readFile)

			if tc.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if string(details.content) != tc.expectedContent {
				t.Errorf("expected content %q, got %q", tc.expectedContent, string(details.content))
			}

			if details.path != tc.expectedPath {
				t.Errorf("expected path %q, got %q", tc.expectedPath, details.path)
			}
		})
	}
}

func TestProcessDetectedBaseImages(t *testing.T) {
	testCases := []struct {
		name          string
		baseImages    map[string]api.ImageStreamTagReference
		image         api.ProjectDirectoryImageBuildStepConfiguration
		details       dockerfileDetails
		expectedSteps []api.StepConfiguration
		expectedImage api.ProjectDirectoryImageBuildStepConfiguration
	}{
		{
			name:       "single registry reference",
			baseImages: map[string]api.ImageStreamTagReference{},
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
			},
			details: dockerfileDetails{
				content: []byte("FROM registry.ci.openshift.org/ocp/4.19:base\nRUN echo hello"),
				path:    "Dockerfile",
			},
			expectedSteps: []api.StepConfiguration{
				{
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Namespace: "ocp",
								Name:      "4.19",
								Tag:       "base",
								As:        "registry.ci.openshift.org/ocp/4.19:base",
							},
							To: api.PipelineImageStreamTagReference("ocp_4.19_base"),
						},
						Sources: []api.ImageStreamSource{{SourceType: "base_image", Name: "ocp_4.19_base"}},
					},
				},
			},
			expectedImage: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"ocp_4.19_base": {
							As: []string{"registry.ci.openshift.org/ocp/4.19:base"},
						},
					},
				},
			},
		},
		{
			name:       "multiple registry references",
			baseImages: map[string]api.ImageStreamTagReference{},
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
			},
			details: dockerfileDetails{
				content: []byte("FROM registry.ci.openshift.org/ocp/4.19:base AS builder\nCOPY --from=registry.ci.openshift.org/ocp/4.19:tools /bin/tool /bin/\nRUN echo hello"),
				path:    "Dockerfile",
			},
			expectedSteps: []api.StepConfiguration{
				{
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Namespace: "ocp",
								Name:      "4.19",
								Tag:       "base",
								As:        "registry.ci.openshift.org/ocp/4.19:base",
							},
							To: api.PipelineImageStreamTagReference("ocp_4.19_base"),
						},
						Sources: []api.ImageStreamSource{{SourceType: "base_image", Name: "ocp_4.19_base"}},
					},
				},
				{
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Namespace: "ocp",
								Name:      "4.19",
								Tag:       "tools",
								As:        "registry.ci.openshift.org/ocp/4.19:tools",
							},
							To: api.PipelineImageStreamTagReference("ocp_4.19_tools"),
						},
						Sources: []api.ImageStreamSource{{SourceType: "base_image", Name: "ocp_4.19_tools"}},
					},
				},
			},
			expectedImage: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"ocp_4.19_base": {
							As: []string{"registry.ci.openshift.org/ocp/4.19:base"},
						},
						"ocp_4.19_tools": {
							As: []string{"registry.ci.openshift.org/ocp/4.19:tools"},
						},
					},
				},
			},
		},
		{
			name:       "no registry references",
			baseImages: map[string]api.ImageStreamTagReference{},
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
			},
			details: dockerfileDetails{
				content: []byte("FROM docker.io/library/golang:1.21\nRUN echo hello"),
				path:    "Dockerfile",
			},
			expectedSteps: nil,
			expectedImage: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
			},
		},
		{
			name:       "skip when manual inputs exist",
			baseImages: map[string]api.ImageStreamTagReference{},
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"custom": {
							As: []string{"registry.ci.openshift.org/ocp/4.19:base"},
						},
					},
				},
			},
			details: dockerfileDetails{
				content: []byte("FROM registry.ci.openshift.org/ocp/4.19:base\nRUN echo hello"),
				path:    "Dockerfile",
			},
			expectedSteps: nil,
			expectedImage: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"custom": {
							As: []string{"registry.ci.openshift.org/ocp/4.19:base"},
						},
					},
				},
			},
		},
		{
			name:       "manual path exist",
			baseImages: map[string]api.ImageStreamTagReference{},
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"custom": {
							Paths: []api.ImageSourcePath{
								{SourcePath: "/custom/path", DestinationDir: "."},
							},
						},
					},
				},
			},
			details: dockerfileDetails{
				content: []byte("FROM registry.ci.openshift.org/ocp/4.19:base\nCOPY /custom/path /custom/path\nRUN echo hello"),
				path:    "Dockerfile",
			},
			expectedSteps: []api.StepConfiguration{
				{
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: api.ImageStreamTagReference{
								Namespace: "ocp",
								Name:      "4.19",
								Tag:       "base",
								As:        "registry.ci.openshift.org/ocp/4.19:base",
							},
							To: api.PipelineImageStreamTagReference("ocp_4.19_base"),
						},
						Sources: []api.ImageStreamSource{{SourceType: "base_image", Name: "ocp_4.19_base"}},
					},
				},
			},
			expectedImage: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"custom": {
							Paths: []api.ImageSourcePath{
								{SourcePath: "/custom/path", DestinationDir: "."},
							},
						},
						"ocp_4.19_base": {
							As: []string{"registry.ci.openshift.org/ocp/4.19:base"},
						},
					},
				},
			},
		},
		{
			name: "use existing base_images",
			baseImages: map[string]api.ImageStreamTagReference{
				"existing": {
					Namespace: "ocp",
					Name:      "4.18",
					Tag:       "base",
				},
			},
			image: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
			},
			details: dockerfileDetails{
				content: []byte("FROM registry.ci.openshift.org/ocp/4.18:base\nRUN echo hello"),
				path:    "Dockerfile",
			},
			expectedSteps: nil,
			expectedImage: api.ProjectDirectoryImageBuildStepConfiguration{
				To: "my-image",
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					Inputs: map[string]api.ImageBuildInputs{
						"existing": {
							As: []string{"registry.ci.openshift.org/ocp/4.18:base"},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			steps, image := processDetectedBaseImages(tc.baseImages, tc.image, tc.details)
			less := func(a, b api.StepConfiguration) bool {
				return string(a.InputImageTagStepConfiguration.To) < string(b.InputImageTagStepConfiguration.To)
			}
			if diff := cmp.Diff(tc.expectedSteps, steps, cmpopts.SortSlices(less)); diff != "" {
				t.Errorf("%s: result didn't match expected, diff: %v", tc.name, diff)
			}
			comparer := func(a, b api.ProjectDirectoryImageBuildStepConfiguration) bool {
				return cmp.Equal(a.ProjectDirectoryImageBuildInputs, b.ProjectDirectoryImageBuildInputs)
			}
			if diff := cmp.Diff(tc.expectedImage, image, cmp.Comparer(comparer)); diff != "" {
				t.Errorf("%s: result didn't match expected, diff: %v", tc.name, diff)
			}
		})
	}
}
