package steps

import (
	"testing"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestCreateBuild(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name            string
		config          api.SourceStepConfiguration
		jobSpec         *api.JobSpec
		clonerefsRef    coreapi.ObjectReference
		resources       api.ResourceConfiguration
		cloneAuthConfig *CloneAuthConfig
		pullSecret      *coreapi.Secret
	}{
		{
			name: "basic options for a presubmit",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/clonerefs",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
						BaseSHA: "masterSHA",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
						}},
					},
				},
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},
		},
		{
			name: "title in pull gets squashed",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/clonerefs",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
						BaseSHA: "masterSHA",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
							Title:  "Revert \"something bad!\"",
						}},
					},
				},
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},
		},
		{
			name: "with a pull secret",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/clonerefs",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
						BaseSHA: "masterSHA",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
						}},
					},
				},
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},
			pullSecret: &coreapi.Secret{
				Data:       map[string][]byte{coreapi.DockerConfigJsonKey: []byte("secret")},
				ObjectMeta: meta.ObjectMeta{Name: PullSecretName},
				Type:       coreapi.SecretTypeDockerConfigJson,
			},
		},
		{
			name: "with a path alias",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/clonerefs",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:       "org",
						Repo:      "repo",
						BaseRef:   "master",
						BaseSHA:   "masterSHA",
						PathAlias: "somewhere/else",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
						}},
					},
				},
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},
		},
		{
			name: "with extra refs",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Namespace: "ci",
					Name:      "managed-clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/clonerefs",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
						BaseSHA: "masterSHA",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
						}},
					},
					ExtraRefs: []prowapi.Refs{{
						Org:     "org",
						Repo:    "other",
						BaseRef: "master",
						BaseSHA: "masterSHA",
					}},
				},
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},
		},
		{
			name: "with extra refs setting workdir and path alias",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/clonerefs",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
						BaseSHA: "masterSHA",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
						}},
					},
					ExtraRefs: []prowapi.Refs{{
						Org:       "org",
						Repo:      "other",
						BaseRef:   "master",
						BaseSHA:   "masterSHA",
						WorkDir:   true,
						PathAlias: "this/is/nuts",
					}},
				},
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},
		},
		{
			name: "with ssh key",
			cloneAuthConfig: &CloneAuthConfig{
				Secret: &coreapi.Secret{
					ObjectMeta: meta.ObjectMeta{Name: "ssh-nykd6bfg"},
				},
				Type: CloneAuthTypeSSH,
			},
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/clonerefs",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
						BaseSHA: "masterSHA",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
						}},
					},
				},
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},
		},

		{

			name: "with OAuth token",
			cloneAuthConfig: &CloneAuthConfig{
				Secret: &coreapi.Secret{
					ObjectMeta: meta.ObjectMeta{Name: "oauth-nykd6bfg"},
				},
				Type: CloneAuthTypeOAuth,
			},
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/clonerefs",
			},
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
						BaseSHA: "masterSHA",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
						}},
					},
				},
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.jobSpec.SetNamespace("namespace")
			actual := createBuild(testCase.config, testCase.jobSpec, testCase.clonerefsRef, testCase.resources, testCase.cloneAuthConfig, testCase.pullSecret, "imagedigest")
			testhelper.CompareWithFixture(t, actual)
		})
	}
}

func TestBuildFromSource(t *testing.T) {
	var testCases = []struct {
		name                          string
		jobSpec                       *api.JobSpec
		fromTag, toTag                api.PipelineImageStreamTagReference
		source                        buildapi.BuildSource
		fromTagDigest, dockerfilePath string
		resources                     api.ResourceConfiguration
		pullSecret                    *coreapi.Secret
		buildArgs                     []api.BuildArg
	}{
		{
			name: "build args: value",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
						BaseSHA: "masterSHA",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
						}},
					},
				},
			},
			buildArgs: []api.BuildArg{{Name: "TAGS", Value: "release"}},
		},
		{
			name: "build args: valueFrom",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job:       "job",
					BuildID:   "buildId",
					ProwJobID: "prowJobId",
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
						BaseSHA: "masterSHA",
						Pulls: []prowapi.Pull{{
							Number: 1,
							SHA:    "pullSHA",
						}},
					},
				},
			},
			buildArgs: []api.BuildArg{{Name: "image-token", ValueFrom: &api.SecretKeySelector{
				Namespace: "test-credentials",
				Name:      "installer-token",
				Key:       "token",
			}}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.jobSpec.SetNamespace("test-namespace")
			actual := buildFromSource(testCase.jobSpec, testCase.fromTag, testCase.toTag, testCase.source, testCase.fromTagDigest, testCase.dockerfilePath, testCase.resources, testCase.pullSecret, testCase.buildArgs)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}
