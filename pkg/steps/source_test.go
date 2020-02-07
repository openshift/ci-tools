package steps

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func strP(str string) *string {
	return &str
}

func TestCreateBuild(t *testing.T) {
	layer := buildapi.ImageOptimizationSkipLayers
	var testCases = []struct {
		name            string
		config          api.SourceStepConfiguration
		jobSpec         *api.JobSpec
		clonerefsRef    coreapi.ObjectReference
		resources       api.ResourceConfiguration
		cloneAuthConfig *CloneAuthConfig
		pullSecret      *coreapi.Secret
		expected        *buildapi.Build
	}{
		{
			name: "basic options for a presubmit",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Cluster:   "https://api.ci.openshift.org",
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
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
				Namespace: "namespace",
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},

			expected: &buildapi.Build{
				ObjectMeta: meta.ObjectMeta{
					Name:      "src",
					Namespace: "namespace",
					Labels: map[string]string{
						"job":                         "job",
						"build-id":                    "buildId",
						"prow.k8s.io/id":              "prowJobId",
						"creates":                     "src",
						"created-by-ci":               "true",
						"ci.openshift.io/refs.org":    "org",
						"ci.openshift.io/refs.repo":   "repo",
						"ci.openshift.io/refs.branch": "master",
					},
					Annotations: map[string]string{
						"ci.openshift.io/job-spec": ``, // set via unexported fields so will be empty
					},
				},
				Spec: buildapi.BuildSpec{
					CommonSpec: buildapi.CommonSpec{
						Resources:      coreapi.ResourceRequirements{Requests: map[coreapi.ResourceName]resource.Quantity{"cpu": resource.MustParse("200m")}},
						ServiceAccount: "builder",
						Source: buildapi.BuildSource{
							Type: buildapi.BuildSourceDockerfile,
							Dockerfile: strP(`
FROM pipeline:root
ADD ./app.binary /clonerefs
RUN umask 0002 && /clonerefs && find /go/src -type d -not -perm -0775 | xargs -r chmod g+xw
WORKDIR /go/src/github.com/org/repo/
ENV GOPATH=/go
RUN git submodule update --init
`),
							Images: []buildapi.ImageSource{
								{
									From: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
									Paths: []buildapi.ImageSourcePath{
										{
											SourcePath:     "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
											DestinationDir: ".",
										},
									},
								},
							},
						},
						Strategy: buildapi.BuildStrategy{
							Type: buildapi.DockerBuildStrategyType,
							DockerStrategy: &buildapi.DockerBuildStrategy{
								DockerfilePath:          "",
								From:                    &coreapi.ObjectReference{Kind: "ImageStreamTag", Namespace: "namespace", Name: "pipeline:root"},
								ForcePull:               true,
								NoCache:                 true,
								Env:                     []coreapi.EnvVar{{Name: "foo", Value: "bar"}, {Name: "CLONEREFS_OPTIONS", Value: `{"src_root":"/go","log":"/dev/null","git_user_name":"ci-robot","git_user_email":"ci-robot@openshift.io","refs":[{"org":"org","repo":"repo","base_ref":"master","base_sha":"masterSHA","pulls":[{"number":1,"author":"","sha":"pullSHA"}]}],"fail":true}`}},
								ImageOptimizationPolicy: &layer,
							},
						},
						Output: buildapi.BuildOutput{
							To: &coreapi.ObjectReference{
								Kind:      "ImageStreamTag",
								Namespace: "namespace",
								Name:      "pipeline:src",
							},
						},
					},
				},
			},
		},
		{
			name: "with a pull secret",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Cluster:   "https://api.ci.openshift.org",
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
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
				Namespace: "namespace",
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},
			pullSecret: &coreapi.Secret{
				Data:       map[string][]byte{coreapi.DockerConfigJsonKey: []byte("secret")},
				ObjectMeta: meta.ObjectMeta{Name: PullSecretName},
				Type:       coreapi.SecretTypeDockerConfigJson,
			},

			expected: &buildapi.Build{
				ObjectMeta: meta.ObjectMeta{
					Name:      "src",
					Namespace: "namespace",
					Labels: map[string]string{
						"job":                         "job",
						"build-id":                    "buildId",
						"prow.k8s.io/id":              "prowJobId",
						"creates":                     "src",
						"created-by-ci":               "true",
						"ci.openshift.io/refs.org":    "org",
						"ci.openshift.io/refs.repo":   "repo",
						"ci.openshift.io/refs.branch": "master",
					},
					Annotations: map[string]string{
						"ci.openshift.io/job-spec": ``, // set via unexported fields so will be empty
					},
				},
				Spec: buildapi.BuildSpec{
					CommonSpec: buildapi.CommonSpec{
						Resources:      coreapi.ResourceRequirements{Requests: map[coreapi.ResourceName]resource.Quantity{"cpu": resource.MustParse("200m")}},
						ServiceAccount: "builder",
						Source: buildapi.BuildSource{
							Type: buildapi.BuildSourceDockerfile,
							Dockerfile: strP(`
FROM pipeline:root
ADD ./app.binary /clonerefs
RUN umask 0002 && /clonerefs && find /go/src -type d -not -perm -0775 | xargs -r chmod g+xw
WORKDIR /go/src/github.com/org/repo/
ENV GOPATH=/go
RUN git submodule update --init
`),
							Images: []buildapi.ImageSource{
								{
									From: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
									Paths: []buildapi.ImageSourcePath{
										{
											SourcePath:     "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
											DestinationDir: ".",
										},
									},
								},
							},
						},
						Strategy: buildapi.BuildStrategy{
							Type: buildapi.DockerBuildStrategyType,
							DockerStrategy: &buildapi.DockerBuildStrategy{
								DockerfilePath:          "",
								From:                    &coreapi.ObjectReference{Kind: "ImageStreamTag", Namespace: "namespace", Name: "pipeline:root"},
								PullSecret:              &coreapi.LocalObjectReference{Name: "regcred"},
								ForcePull:               true,
								NoCache:                 true,
								Env:                     []coreapi.EnvVar{{Name: "foo", Value: "bar"}, {Name: "CLONEREFS_OPTIONS", Value: `{"src_root":"/go","log":"/dev/null","git_user_name":"ci-robot","git_user_email":"ci-robot@openshift.io","refs":[{"org":"org","repo":"repo","base_ref":"master","base_sha":"masterSHA","pulls":[{"number":1,"author":"","sha":"pullSHA"}]}],"fail":true}`}},
								ImageOptimizationPolicy: &layer,
							},
						},
						Output: buildapi.BuildOutput{
							To: &coreapi.ObjectReference{
								Kind:      "ImageStreamTag",
								Namespace: "namespace",
								Name:      "pipeline:src",
							},
						},
					},
				},
			},
		},
		{
			name: "with a path alias",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Cluster:   "https://api.ci.openshift.org",
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
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
				Namespace: "namespace",
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},

			expected: &buildapi.Build{
				ObjectMeta: meta.ObjectMeta{
					Name:      "src",
					Namespace: "namespace",
					Labels: map[string]string{
						"job":                         "job",
						"build-id":                    "buildId",
						"prow.k8s.io/id":              "prowJobId",
						"creates":                     "src",
						"created-by-ci":               "true",
						"ci.openshift.io/refs.org":    "org",
						"ci.openshift.io/refs.repo":   "repo",
						"ci.openshift.io/refs.branch": "master",
					},
					Annotations: map[string]string{
						"ci.openshift.io/job-spec": ``, // set via unexported fields so will be empty
					},
				},
				Spec: buildapi.BuildSpec{
					CommonSpec: buildapi.CommonSpec{
						Resources:      coreapi.ResourceRequirements{Requests: map[coreapi.ResourceName]resource.Quantity{"cpu": resource.MustParse("200m")}},
						ServiceAccount: "builder",
						Source: buildapi.BuildSource{
							Type: buildapi.BuildSourceDockerfile,
							Dockerfile: strP(`
FROM pipeline:root
ADD ./app.binary /clonerefs
RUN umask 0002 && /clonerefs && find /go/src -type d -not -perm -0775 | xargs -r chmod g+xw
WORKDIR /go/src/somewhere/else/
ENV GOPATH=/go
RUN git submodule update --init
`),
							Images: []buildapi.ImageSource{
								{
									From: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
									Paths: []buildapi.ImageSourcePath{
										{
											SourcePath:     "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
											DestinationDir: ".",
										},
									},
								},
							},
						},
						Strategy: buildapi.BuildStrategy{
							Type: buildapi.DockerBuildStrategyType,
							DockerStrategy: &buildapi.DockerBuildStrategy{
								DockerfilePath:          "",
								From:                    &coreapi.ObjectReference{Kind: "ImageStreamTag", Namespace: "namespace", Name: "pipeline:root"},
								ForcePull:               true,
								NoCache:                 true,
								Env:                     []coreapi.EnvVar{{Name: "foo", Value: "bar"}, {Name: "CLONEREFS_OPTIONS", Value: `{"src_root":"/go","log":"/dev/null","git_user_name":"ci-robot","git_user_email":"ci-robot@openshift.io","refs":[{"org":"org","repo":"repo","base_ref":"master","base_sha":"masterSHA","pulls":[{"number":1,"author":"","sha":"pullSHA"}],"path_alias":"somewhere/else"}],"fail":true}`}},
								ImageOptimizationPolicy: &layer,
							},
						},
						Output: buildapi.BuildOutput{
							To: &coreapi.ObjectReference{
								Kind:      "ImageStreamTag",
								Namespace: "namespace",
								Name:      "pipeline:src",
							},
						},
					},
				},
			},
		},
		{
			name: "with extra refs",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Cluster:   "https://api.ci.openshift.org",
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
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
				Namespace: "namespace",
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},

			expected: &buildapi.Build{
				ObjectMeta: meta.ObjectMeta{
					Name:      "src",
					Namespace: "namespace",
					Labels: map[string]string{
						"job":                         "job",
						"build-id":                    "buildId",
						"prow.k8s.io/id":              "prowJobId",
						"creates":                     "src",
						"created-by-ci":               "true",
						"ci.openshift.io/refs.org":    "org",
						"ci.openshift.io/refs.repo":   "repo",
						"ci.openshift.io/refs.branch": "master",
					},
					Annotations: map[string]string{
						"ci.openshift.io/job-spec": ``, // set via unexported fields so will be empty
					},
				},
				Spec: buildapi.BuildSpec{
					CommonSpec: buildapi.CommonSpec{
						Resources:      coreapi.ResourceRequirements{Requests: map[coreapi.ResourceName]resource.Quantity{"cpu": resource.MustParse("200m")}},
						ServiceAccount: "builder",
						Source: buildapi.BuildSource{
							Type: buildapi.BuildSourceDockerfile,
							Dockerfile: strP(`
FROM pipeline:root
ADD ./app.binary /clonerefs
RUN umask 0002 && /clonerefs && find /go/src -type d -not -perm -0775 | xargs -r chmod g+xw
WORKDIR /go/src/github.com/org/repo/
ENV GOPATH=/go
RUN git submodule update --init
`),
							Images: []buildapi.ImageSource{
								{
									From: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
									Paths: []buildapi.ImageSourcePath{
										{
											SourcePath:     "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
											DestinationDir: ".",
										},
									},
								},
							},
						},
						Strategy: buildapi.BuildStrategy{
							Type: buildapi.DockerBuildStrategyType,
							DockerStrategy: &buildapi.DockerBuildStrategy{
								DockerfilePath:          "",
								From:                    &coreapi.ObjectReference{Kind: "ImageStreamTag", Namespace: "namespace", Name: "pipeline:root"},
								ForcePull:               true,
								NoCache:                 true,
								Env:                     []coreapi.EnvVar{{Name: "foo", Value: "bar"}, {Name: "CLONEREFS_OPTIONS", Value: `{"src_root":"/go","log":"/dev/null","git_user_name":"ci-robot","git_user_email":"ci-robot@openshift.io","refs":[{"org":"org","repo":"repo","base_ref":"master","base_sha":"masterSHA","pulls":[{"number":1,"author":"","sha":"pullSHA"}]},{"org":"org","repo":"other","base_ref":"master","base_sha":"masterSHA"}],"fail":true}`}},
								ImageOptimizationPolicy: &layer,
							},
						},
						Output: buildapi.BuildOutput{
							To: &coreapi.ObjectReference{
								Kind:      "ImageStreamTag",
								Namespace: "namespace",
								Name:      "pipeline:src",
							},
						},
					},
				},
			},
		},
		{
			name: "with extra refs setting workdir and path alias",
			config: api.SourceStepConfiguration{
				From: api.PipelineImageStreamTagReferenceRoot,
				To:   api.PipelineImageStreamTagReferenceSource,
				ClonerefsImage: api.ImageStreamTagReference{
					Cluster:   "https://api.ci.openshift.org",
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
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
				Namespace: "namespace",
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},

			expected: &buildapi.Build{
				ObjectMeta: meta.ObjectMeta{
					Name:      "src",
					Namespace: "namespace",
					Labels: map[string]string{
						"job":                         "job",
						"build-id":                    "buildId",
						"prow.k8s.io/id":              "prowJobId",
						"creates":                     "src",
						"created-by-ci":               "true",
						"ci.openshift.io/refs.org":    "org",
						"ci.openshift.io/refs.repo":   "repo",
						"ci.openshift.io/refs.branch": "master",
					},
					Annotations: map[string]string{
						"ci.openshift.io/job-spec": ``, // set via unexported fields so will be empty
					},
				},
				Spec: buildapi.BuildSpec{
					CommonSpec: buildapi.CommonSpec{
						Resources:      coreapi.ResourceRequirements{Requests: map[coreapi.ResourceName]resource.Quantity{"cpu": resource.MustParse("200m")}},
						ServiceAccount: "builder",
						Source: buildapi.BuildSource{
							Type: buildapi.BuildSourceDockerfile,
							Dockerfile: strP(`
FROM pipeline:root
ADD ./app.binary /clonerefs
RUN umask 0002 && /clonerefs && find /go/src -type d -not -perm -0775 | xargs -r chmod g+xw
WORKDIR /go/src/this/is/nuts/
ENV GOPATH=/go
RUN git submodule update --init
`),
							Images: []buildapi.ImageSource{
								{
									From: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
									Paths: []buildapi.ImageSourcePath{
										{
											SourcePath:     "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
											DestinationDir: ".",
										},
									},
								},
							},
						},
						Strategy: buildapi.BuildStrategy{
							Type: buildapi.DockerBuildStrategyType,
							DockerStrategy: &buildapi.DockerBuildStrategy{
								DockerfilePath:          "",
								From:                    &coreapi.ObjectReference{Kind: "ImageStreamTag", Namespace: "namespace", Name: "pipeline:root"},
								ForcePull:               true,
								NoCache:                 true,
								Env:                     []coreapi.EnvVar{{Name: "foo", Value: "bar"}, {Name: "CLONEREFS_OPTIONS", Value: `{"src_root":"/go","log":"/dev/null","git_user_name":"ci-robot","git_user_email":"ci-robot@openshift.io","refs":[{"org":"org","repo":"repo","base_ref":"master","base_sha":"masterSHA","pulls":[{"number":1,"author":"","sha":"pullSHA"}]},{"org":"org","repo":"other","base_ref":"master","base_sha":"masterSHA","path_alias":"this/is/nuts","workdir":true}],"fail":true}`}},
								ImageOptimizationPolicy: &layer,
							},
						},
						Output: buildapi.BuildOutput{
							To: &coreapi.ObjectReference{
								Kind:      "ImageStreamTag",
								Namespace: "namespace",
								Name:      "pipeline:src",
							},
						},
					},
				},
			},
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
					Cluster:   "https://api.ci.openshift.org",
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
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
				Namespace: "namespace",
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},

			expected: &buildapi.Build{
				ObjectMeta: meta.ObjectMeta{
					Name:      "src",
					Namespace: "namespace",
					Labels: map[string]string{
						"job":                         "job",
						"build-id":                    "buildId",
						"prow.k8s.io/id":              "prowJobId",
						"creates":                     "src",
						"created-by-ci":               "true",
						"ci.openshift.io/refs.org":    "org",
						"ci.openshift.io/refs.repo":   "repo",
						"ci.openshift.io/refs.branch": "master",
					},
					Annotations: map[string]string{
						"ci.openshift.io/job-spec": ``, // set via unexported fields so will be empty
					},
				},
				Spec: buildapi.BuildSpec{
					CommonSpec: buildapi.CommonSpec{
						Resources:      coreapi.ResourceRequirements{Requests: map[coreapi.ResourceName]resource.Quantity{"cpu": resource.MustParse("200m")}},
						ServiceAccount: "builder",
						Source: buildapi.BuildSource{
							Type: buildapi.BuildSourceDockerfile,
							Dockerfile: strP(`
FROM pipeline:root
ADD ./app.binary /clonerefs
ADD /ssh_config /etc/ssh/ssh_config
COPY ./ssh-privatekey /sshprivatekey
RUN umask 0002 && /clonerefs && find /go/src -type d -not -perm -0775 | xargs -r chmod g+xw
WORKDIR /go/src/github.com/org/repo/
ENV GOPATH=/go
RUN git submodule update --init
RUN rm -f /sshprivatekey
`),
							Images: []buildapi.ImageSource{
								{
									From: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
									Paths: []buildapi.ImageSourcePath{
										{
											SourcePath:     "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
											DestinationDir: ".",
										},
										{
											SourcePath:     "/ssh_config",
											DestinationDir: ".",
										},
									},
								},
							},
							Secrets: []buildapi.SecretBuildSource{
								{
									Secret: coreapi.LocalObjectReference{Name: "ssh-nykd6bfg"},
								},
							},
						},
						Strategy: buildapi.BuildStrategy{
							Type: buildapi.DockerBuildStrategyType,
							DockerStrategy: &buildapi.DockerBuildStrategy{
								DockerfilePath:          "",
								From:                    &coreapi.ObjectReference{Kind: "ImageStreamTag", Namespace: "namespace", Name: "pipeline:root"},
								ForcePull:               true,
								NoCache:                 true,
								Env:                     []coreapi.EnvVar{{Name: "foo", Value: "bar"}, {Name: "CLONEREFS_OPTIONS", Value: `{"src_root":"/go","log":"/dev/null","git_user_name":"ci-robot","git_user_email":"ci-robot@openshift.io","refs":[{"org":"org","repo":"repo","base_ref":"master","base_sha":"masterSHA","pulls":[{"number":1,"author":"","sha":"pullSHA"}],"clone_uri":"ssh://git@github.com/org/repo.git"}],"key_files":["/sshprivatekey"],"fail":true}`}},
								ImageOptimizationPolicy: &layer,
							},
						},
						Output: buildapi.BuildOutput{
							To: &coreapi.ObjectReference{
								Kind:      "ImageStreamTag",
								Namespace: "namespace",
								Name:      "pipeline:src",
							},
						},
					},
				},
			},
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
					Cluster:   "https://api.ci.openshift.org",
					Namespace: "ci",
					Name:      "clonerefs",
					Tag:       "latest",
				},
				ClonerefsPath: "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
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
				Namespace: "namespace",
			},
			clonerefsRef: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
			resources:    map[string]api.ResourceRequirements{"*": {Requests: map[string]string{"cpu": "200m"}}},

			expected: &buildapi.Build{
				ObjectMeta: meta.ObjectMeta{
					Name:      "src",
					Namespace: "namespace",
					Labels: map[string]string{
						"job":                         "job",
						"build-id":                    "buildId",
						"prow.k8s.io/id":              "prowJobId",
						"creates":                     "src",
						"created-by-ci":               "true",
						"ci.openshift.io/refs.org":    "org",
						"ci.openshift.io/refs.repo":   "repo",
						"ci.openshift.io/refs.branch": "master",
					},
					Annotations: map[string]string{
						"ci.openshift.io/job-spec": ``, // set via unexported fields so will be empty
					},
				},
				Spec: buildapi.BuildSpec{
					CommonSpec: buildapi.CommonSpec{
						Resources:      coreapi.ResourceRequirements{Requests: map[coreapi.ResourceName]resource.Quantity{"cpu": resource.MustParse("200m")}},
						ServiceAccount: "builder",
						Source: buildapi.BuildSource{
							Type: buildapi.BuildSourceDockerfile,
							Dockerfile: strP(`
FROM pipeline:root
ADD ./app.binary /clonerefs
COPY ./oauth-token /oauth-token
RUN umask 0002 && /clonerefs && find /go/src -type d -not -perm -0775 | xargs -r chmod g+xw
WORKDIR /go/src/github.com/org/repo/
ENV GOPATH=/go
RUN git submodule update --init
RUN rm -f /oauth-token
`),
							Images: []buildapi.ImageSource{
								{
									From: coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "clonerefs:latest", Namespace: "ci"},
									Paths: []buildapi.ImageSourcePath{
										{
											SourcePath:     "/app/prow/cmd/clonerefs/app.binary.runfiles/io_k8s_test_infra/prow/cmd/clonerefs/linux_amd64_pure_stripped/app.binary",
											DestinationDir: ".",
										},
									},
								},
							},
							Secrets: []buildapi.SecretBuildSource{
								{
									Secret: coreapi.LocalObjectReference{Name: "oauth-nykd6bfg"},
								},
							},
						},
						Strategy: buildapi.BuildStrategy{
							Type: buildapi.DockerBuildStrategyType,
							DockerStrategy: &buildapi.DockerBuildStrategy{
								DockerfilePath:          "",
								From:                    &coreapi.ObjectReference{Kind: "ImageStreamTag", Namespace: "namespace", Name: "pipeline:root"},
								ForcePull:               true,
								NoCache:                 true,
								Env:                     []coreapi.EnvVar{{Name: "foo", Value: "bar"}, {Name: "CLONEREFS_OPTIONS", Value: `{"src_root":"/go","log":"/dev/null","git_user_name":"ci-robot","git_user_email":"ci-robot@openshift.io","refs":[{"org":"org","repo":"repo","base_ref":"master","base_sha":"masterSHA","pulls":[{"number":1,"author":"","sha":"pullSHA"}],"clone_uri":"https://github.com/org/repo.git"}],"oauth_token_file":"/oauth-token","fail":true}`}},
								ImageOptimizationPolicy: &layer,
							},
						},
						Output: buildapi.BuildOutput{
							To: &coreapi.ObjectReference{
								Kind:      "ImageStreamTag",
								Namespace: "namespace",
								Name:      "pipeline:src",
							},
						},
					},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := createBuild(testCase.config, testCase.jobSpec, testCase.clonerefsRef, testCase.resources, testCase.cloneAuthConfig, testCase.pullSecret), testCase.expected; !equality.Semantic.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect build: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
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
