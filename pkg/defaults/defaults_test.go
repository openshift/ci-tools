package defaults

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	"k8s.io/utils/diff"

	imageapi "github.com/openshift/api/image/v1"
	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	fakectrlruntimeclient "github.com/openshift/ci-tools/pkg/util/watchingclient/fake"
)

func addCloneRefs(cfg *api.SourceStepConfiguration) *api.SourceStepConfiguration {
	cfg.ClonerefsImage = api.ImageStreamTagReference{Namespace: "ci", Name: "managed-clonerefs", Tag: "latest"}
	cfg.ClonerefsPath = "/clonerefs"
	return cfg
}

func TestStepConfigsForBuild(t *testing.T) {
	noopResolver := func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error) {
		return root, nil
	}
	var testCases = []struct {
		name     string
		input    *api.ReleaseBuildConfiguration
		jobSpec  *api.JobSpec
		output   []api.StepConfiguration
		readFile readFile
		resolver resolveRoot
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "root-ns",
						Name:      "root-name",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceRoot,
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "build-cache",
						Name:      "org-repo",
						Tag:       "branch",
					},
					To: api.PipelineImageStreamTagReferenceRoot,
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "stream-namespace",
						Name:      "stream-name",
						Tag:       "stream-tag",
					},
					To: api.PipelineImageStreamTagReferenceRoot,
				},
			}},
			readFile: func(filename string) ([]byte, error) {
				if filename != ".ci-operator.yaml" {
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "root-ns",
						Name:      "root-name",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceRoot,
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "root-ns",
						Name:      "root-name",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceRoot,
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "root-ns",
						Name:      "root-name",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceRoot,
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "root-ns",
						Name:      "root-name",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceRoot,
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "root-ns",
						Name:      "root-name",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceRoot,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: "namespace",
						Name:      "name",
						Tag:       "tag",
						As:        "name",
					},
					To: api.PipelineImageStreamTagReference("name"),
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
						BaseImage: api.ImageStreamTagReference{
							Namespace: "root-ns",
							Name:      "root-name",
							Tag:       "manual",
						},
						To: api.PipelineImageStreamTagReferenceRoot,
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
						BaseImage: api.ImageStreamTagReference{
							Namespace: "test",
							Name:      "other",
							Tag:       "tag",
							As:        "name",
						},
						To: api.PipelineImageStreamTagReference("name"),
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "root-ns",
						Name:      "root-name",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceRoot,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: "namespace",
						Name:      "name",
						Tag:       "tag",
						As:        "name",
					},
					To: api.PipelineImageStreamTagReference("name-without-rpms"),
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "root-ns",
						Name:      "root-name",
						Tag:       "manual",
					},
					To: "root",
				},
			}, {
				ProjectDirectoryImageBuildStepConfiguration: &api.ProjectDirectoryImageBuildStepConfiguration{
					To:                               "ci-index",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{DockerfilePath: "index.Dockerfile"},
				},
			}, {
				ProjectDirectoryImageBuildStepConfiguration: &api.ProjectDirectoryImageBuildStepConfiguration{
					To: "ci-bundle0",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "manifests/olm",
						DockerfilePath: "bundle.Dockerfile",
					},
				},
			}, {
				SourceStepConfiguration: &api.SourceStepConfiguration{
					From:           "root",
					To:             "src",
					ClonerefsImage: api.ImageStreamTagReference{Namespace: "ci", Name: "managed-clonerefs", Tag: "latest"},
					ClonerefsPath:  "/clonerefs",
				},
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
					BaseImage: api.ImageStreamTagReference{
						Namespace: "root-ns",
						Name:      "root-name",
						Tag:       "manual",
					},
					To: "root",
				},
			}, {
				ProjectDirectoryImageBuildStepConfiguration: &api.ProjectDirectoryImageBuildStepConfiguration{
					To:                               "ci-index-my-bundle",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{DockerfilePath: "index.Dockerfile"},
				},
			}, {
				ProjectDirectoryImageBuildStepConfiguration: &api.ProjectDirectoryImageBuildStepConfiguration{
					To: "my-bundle",
					ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
						ContextDir:     "manifests/olm",
						DockerfilePath: "bundle.Dockerfile",
					},
				},
			}, {
				SourceStepConfiguration: &api.SourceStepConfiguration{
					From:           "root",
					To:             "src",
					ClonerefsImage: api.ImageStreamTagReference{Namespace: "ci", Name: "managed-clonerefs", Tag: "latest"},
					ClonerefsPath:  "/clonerefs",
				},
			}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rawSteps, err := stepConfigsForBuild(testCase.input, testCase.jobSpec, testCase.readFile, testCase.resolver)
			if err != nil {
				t.Fatalf("failed to get stepConfigsForBuild: %v", err)
			}
			actual := sortStepConfig(rawSteps)
			expected := sortStepConfig(testCase.output)
			if diff := cmp.Diff(actual, expected); diff != "" {
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
		content := `{"nodes": [{"version": "version", "payload": "payload"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       ioutil.NopCloser(bytes.NewBuffer([]byte(content))),
		}, nil
	})
	client := loggingclient.New(fakectrlruntimeclient.NewFakeClient())
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
			"src", "bin", "to",
			"ci-bundle0", "ci-index",
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
	buildClient := steps.NewBuildClient(client, nil)
	var templateClient steps.TemplateClient
	podClient := steps.NewPodClient(client, nil, nil)
	var leaseClient *lease.Client
	var requiredTargets []string
	var cloneAuthConfig *steps.CloneAuthConfig
	pullSecret, pushSecret := &coreapi.Secret{}, &coreapi.Secret{}
	for _, tc := range []struct {
		name           string
		config         api.ReleaseBuildConfiguration
		refs           *prowapi.Refs
		paramFiles     string
		promote        bool
		templates      []*templateapi.Template
		env            api.Parameters
		params         map[string]string
		expectedSteps  []string
		expectedPost   []string
		expectedParams map[string]string
		expectedErr    error
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
			"IMAGE_FORMAT":          "public_docker_image_repository/ns/stable:${component}",
			"RELEASE_IMAGE_INITIAL": "public_docker_image_repository:initial",
			"RELEASE_IMAGE_LATEST":  "public_docker_image_repository:latest",
		},
	}, {
		name: "resolve release",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				Releases: map[string]api.UnresolvedRelease{
					"release": {Release: &api.Release{Version: "version"}},
				},
			},
		},
		expectedSteps: []string{"[release:release]", "[images]"},
		expectedParams: map[string]string{
			utils.ReleaseImageEnv("release"): "public_docker_image_repository:release",
		},
	}, {
		name: "resolve release with input",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				Releases: map[string]api.UnresolvedRelease{
					"release": {Release: &api.Release{Version: "version"}},
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
			"CLUSTER_TYPE":        "aws",
			steps.DefaultLeaseEnv: "",
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
				Namespace: ns,
				Name:      "name",
				Tag:       "tag",
			},
		},
		promote:       true,
		expectedSteps: []string{"[output-images]", "[images]"},
		expectedPost:  []string{"[promotion]"},
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
	}} {
		t.Run(tc.name, func(t *testing.T) {
			jobSpec := api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:  "job_name",
					Refs: tc.refs,
				},
			}
			jobSpec.SetNamespace(ns)
			params := api.NewDeferredParameters(tc.env)
			for k, v := range tc.params {
				params.Add(k, func() (string, error) { return v, nil })
			}
			steps, post, err := fromConfig(context.Background(), &tc.config, &jobSpec, tc.templates, tc.paramFiles, tc.promote, client, buildClient, templateClient, podClient, leaseClient, httpClient, requiredTargets, cloneAuthConfig, pullSecret, pushSecret, params)
			if diff := cmp.Diff(tc.expectedErr, err); diff != "" {
				t.Errorf("unexpected error: %v", diff)
			}
			var stepNames, postNames []string
			for _, s := range steps {
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
				"NAMESPACE":     ns,
			} {
				tc.expectedParams[k] = v
			}
			if diff := cmp.Diff(tc.expectedSteps, stepNames); diff != "" {
				t.Errorf("unexpected steps: %v", diff)
			}
			if diff := cmp.Diff(tc.expectedPost, postNames); diff != "" {
				t.Errorf("unexpected post steps: %v", diff)
			}
			if diff := cmp.Diff(tc.expectedParams, paramMap); diff != "" {
				t.Errorf("unexpected parameters: %v", diff)
			}
		})
	}
}

func TestLeasesForTest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tests    api.MultiStageTestConfigurationLiteral
		expected []api.StepLease
	}{{
		name:  "no configuration or cluster profile, no lease",
		tests: api.MultiStageTestConfigurationLiteral{},
	}, {
		name: "cluster profile, lease",
		tests: api.MultiStageTestConfigurationLiteral{
			ClusterProfile: api.ClusterProfileAWS,
		},
		expected: []api.StepLease{{
			ResourceType: "aws-quota-slice",
			Env:          steps.DefaultLeaseEnv,
			Count:        1,
		}},
	}, {
		name: "explicit configuration, lease",
		tests: api.MultiStageTestConfigurationLiteral{
			Leases: []api.StepLease{{ResourceType: "aws-quota-slice"}},
		},
		expected: []api.StepLease{{ResourceType: "aws-quota-slice"}},
	}, {
		name: "explicit configuration in step, lease",
		tests: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{
				{Leases: []api.StepLease{{ResourceType: "aws-quota-slice"}}},
			},
		},
		expected: []api.StepLease{{ResourceType: "aws-quota-slice"}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret := leasesForTest(&tc.tests)
			if diff := diff.ObjectReflectDiff(tc.expected, ret); diff != "<no diffs>" {
				t.Errorf("incorrect leases: %s", diff)
			}
		})
	}
}
