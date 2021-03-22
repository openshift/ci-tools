package steps

import (
	"reflect"
	"testing"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

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

func TestDefaultPodLabels(t *testing.T) {
	testCases := []struct {
		id             string
		jobSpec        *api.JobSpec
		expectedLabels map[string]string
	}{
		{
			id: "Refs defined, expected labels with org/repo/branch information",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
					},
				},
			},
			expectedLabels: map[string]string{
				"OPENSHIFT_CI":                "true",
				"created-by-ci":               "true",
				"prow.k8s.io/id":              "",
				"build-id":                    "",
				"job":                         "",
				"ci.openshift.io/refs.org":    "org",
				"ci.openshift.io/refs.repo":   "repo",
				"ci.openshift.io/refs.branch": "master",
			},
		},
		{
			id: "nil Refs, expected labels without org/repo/branch information",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: nil,
				},
			},
			expectedLabels: map[string]string{
				"OPENSHIFT_CI":   "true",
				"created-by-ci":  "true",
				"prow.k8s.io/id": "",
				"build-id":       "",
				"job":            "",
			},
		},
		{
			id: "nil Refs but ExtraRefs is > 0, expected labels with extraref[0] org/repo/branch information",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: nil,
					ExtraRefs: []prowapi.Refs{
						{
							Org:     "extraorg",
							Repo:    "extrarepo",
							BaseRef: "master",
						},
					},
				},
			},
			expectedLabels: map[string]string{
				"OPENSHIFT_CI":                "true",
				"created-by-ci":               "true",
				"prow.k8s.io/id":              "",
				"build-id":                    "",
				"job":                         "",
				"ci.openshift.io/refs.org":    "extraorg",
				"ci.openshift.io/refs.repo":   "extrarepo",
				"ci.openshift.io/refs.branch": "master",
			},
		},
		{
			id: "non-nil Refs and ExtraRefs is > 0, expected labels with refs org/repo/branch information",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "master",
					},
					ExtraRefs: []prowapi.Refs{
						{
							Org:     "extraorg",
							Repo:    "extrarepo",
							BaseRef: "master",
						},
					},
				},
			},
			expectedLabels: map[string]string{
				"OPENSHIFT_CI":                "true",
				"created-by-ci":               "true",
				"prow.k8s.io/id":              "",
				"build-id":                    "",
				"job":                         "",
				"ci.openshift.io/refs.org":    "org",
				"ci.openshift.io/refs.repo":   "repo",
				"ci.openshift.io/refs.branch": "master",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			labels := defaultPodLabels(tc.jobSpec)
			if !reflect.DeepEqual(labels, tc.expectedLabels) {
				t.Fatal(diff.ObjectReflectDiff(labels, tc.expectedLabels))
			}
		})
	}
}
