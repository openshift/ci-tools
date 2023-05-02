package steps

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
	testhelper_kube "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
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
				ObjectMeta: meta.ObjectMeta{Name: api.RegistryPullCredentialsSecret},
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
			name: "build args",
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
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.jobSpec.SetNamespace("test-namespace")
			actual := buildFromSource(testCase.jobSpec, testCase.fromTag, testCase.toTag, testCase.source, testCase.fromTagDigest, testCase.dockerfilePath, testCase.resources, testCase.pullSecret, testCase.buildArgs)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}

func init() {
	if err := buildapi.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add buildapi to scheme: %v", err))
	}
}

func TestWaitForBuild(t *testing.T) {
	ns := "ns"
	now := meta.Time{Time: time.Now()}
	start, end := meta.Time{Time: now.Time.Add(-3 * time.Second)}, now
	var testCases = []struct {
		name        string
		buildClient BuildClient
		timeout     time.Duration
		expected    error
	}{
		{
			name: "timeout",
			buildClient: NewBuildClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&buildapi.Build{
					ObjectMeta: meta.ObjectMeta{
						Name:              "some-build",
						Namespace:         ns,
						CreationTimestamp: start,
						Annotations: map[string]string{
							buildapi.BuildPodNameAnnotation: "some-build-build",
						},
					},
					Status: buildapi.BuildStatus{
						Phase:               buildapi.BuildPhasePending,
						StartTimestamp:      &start,
						CompletionTimestamp: &end,
					},
				},
			).Build()), nil, nil),
			expected: fmt.Errorf("build didn't start running within 0s (phase: Pending)"),
		},
		{
			name: "timeout with pod",
			buildClient: NewBuildClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&buildapi.Build{
					ObjectMeta: meta.ObjectMeta{
						Name:              "some-build",
						Namespace:         ns,
						CreationTimestamp: start,
						Annotations: map[string]string{
							buildapi.BuildPodNameAnnotation: "some-build-build",
						},
					},
					Status: buildapi.BuildStatus{
						Phase:               buildapi.BuildPhasePending,
						StartTimestamp:      &start,
						CompletionTimestamp: &end,
					},
				},
				&coreapi.Pod{
					ObjectMeta: meta.ObjectMeta{
						Name:      "some-build-build",
						Namespace: ns,
					},
				},
			).Build()), nil, nil),
			expected: fmt.Errorf("build didn't start running within 0s (phase: Pending):\nFound 0 events for Pod some-build-build:"),
		},
		{
			name: "timeout with pod and events",
			buildClient: NewBuildClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&buildapi.Build{
					ObjectMeta: meta.ObjectMeta{
						Name:              "some-build",
						Namespace:         ns,
						CreationTimestamp: start,
						Annotations: map[string]string{
							buildapi.BuildPodNameAnnotation: "some-build-build",
						},
					},
					Status: buildapi.BuildStatus{
						Phase:               buildapi.BuildPhasePending,
						StartTimestamp:      &start,
						CompletionTimestamp: &end,
					},
				},
				&coreapi.Pod{
					ObjectMeta: meta.ObjectMeta{
						Name:      "some-build-build",
						Namespace: ns,
						UID:       "UID",
					},
					Status: coreapi.PodStatus{
						ContainerStatuses: []coreapi.ContainerStatus{{
							Name: "the-container",
							State: coreapi.ContainerState{
								Waiting: &coreapi.ContainerStateWaiting{
									Reason:  "the_reason",
									Message: "the_message",
								},
							},
						}},
					},
				},
			).Build()), nil, nil),
			expected: fmt.Errorf(`build didn't start running within 0s (phase: Pending):
* Container the-container is not ready with reason the_reason and message the_message
Found 0 events for Pod some-build-build:`),
		},
		{
			name: "build succeeded",
			buildClient: NewBuildClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&buildapi.Build{
					ObjectMeta: meta.ObjectMeta{
						Name:              "some-build",
						Namespace:         ns,
						CreationTimestamp: start,
					},
					Status: buildapi.BuildStatus{
						Phase:               buildapi.BuildPhaseComplete,
						StartTimestamp:      &start,
						CompletionTimestamp: &end,
					},
				}).Build()), nil, nil),
			timeout: 30 * time.Minute,
		},
		{
			name: "build failed",
			buildClient: NewFakeBuildClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&buildapi.Build{
					ObjectMeta: meta.ObjectMeta{
						Name:              "some-build",
						Namespace:         ns,
						CreationTimestamp: start,
					},
					Status: buildapi.BuildStatus{
						Phase:               buildapi.BuildPhaseCancelled,
						Reason:              "reason",
						Message:             "msg",
						LogSnippet:          "snippet",
						StartTimestamp:      &start,
						CompletionTimestamp: &end,
					},
				}).Build()), "abc\n"), // the line break is for gotestsum https://github.com/gotestyourself/gotestsum/issues/141#issuecomment-1209146526
			timeout:  30 * time.Minute,
			expected: fmt.Errorf("%s\n\n%s", "the build some-build failed after 3s with reason reason: msg", "snippet"),
		},
		{
			name: "build already succeeded",
			buildClient: NewBuildClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&buildapi.Build{
					ObjectMeta: meta.ObjectMeta{
						Name:      "some-build",
						Namespace: ns,
						CreationTimestamp: meta.Time{
							Time: now.Add(-60 * time.Minute),
						},
					},
					Status: buildapi.BuildStatus{
						Phase: buildapi.BuildPhaseComplete,
						StartTimestamp: &meta.Time{
							Time: now.Add(-60 * time.Minute),
						},
						CompletionTimestamp: &meta.Time{
							Time: now.Add(-59 * time.Minute),
						},
					},
				}).Build()), nil, nil),
			timeout: 30 * time.Minute,
		},
		{
			name: "build already failed",
			buildClient: NewFakeBuildClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&buildapi.Build{
					ObjectMeta: meta.ObjectMeta{
						Name:      "some-build",
						Namespace: ns,
						CreationTimestamp: meta.Time{
							Time: now.Add(-60 * time.Minute),
						},
					},
					Status: buildapi.BuildStatus{
						Phase:      buildapi.BuildPhaseCancelled,
						Reason:     "reason",
						Message:    "msg",
						LogSnippet: "snippet",
						StartTimestamp: &meta.Time{
							Time: now.Add(-60 * time.Minute),
						},
						CompletionTimestamp: &meta.Time{
							Time: now.Add(-59 * time.Minute),
						},
					},
				}).Build()), "abc\n"),
			timeout:  30 * time.Minute,
			expected: fmt.Errorf("%s\n\n%s", "the build some-build failed after 1m0s with reason reason: msg", "snippet"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			client := testhelper_kube.FakePodClient{
				FakePodExecutor: &testhelper_kube.FakePodExecutor{
					LoggingClient: testCase.buildClient,
				},
				PendingTimeout: testCase.timeout,
			}
			actual := waitForBuild(context.TODO(), testCase.buildClient, &client, ns, "some-build")
			if diff := cmp.Diff(testCase.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: mismatch (-expected +actual), diff: %s", testCase.name, diff)
			}
		})
	}
}

type fakeBuildClient struct {
	loggingclient.LoggingClient
	logContent        string
	nodeArchitectures []string
}

func NewFakeBuildClient(client loggingclient.LoggingClient, logContent string) BuildClient {
	return &fakeBuildClient{
		LoggingClient: client,
		logContent:    logContent,
	}
}

func (c *fakeBuildClient) Logs(namespace, name string, options *buildapi.BuildLogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(c.logContent)), nil
}

func (c *fakeBuildClient) NodeArchitectures() []string {
	return c.nodeArchitectures
}

func Test_constructMultiArchBuilds(t *testing.T) {
	tests := []struct {
		name              string
		build             buildapi.Build
		nodeArchitectures []string
		want              []buildapi.Build
	}{
		{
			name:              "basic case - only amd64",
			nodeArchitectures: []string{"amd64"},
			build: buildapi.Build{
				ObjectMeta: meta.ObjectMeta{Name: "test-build"},
			},
			want: []buildapi.Build{
				{
					ObjectMeta: meta.ObjectMeta{Name: "test-build"},
					Spec: buildapi.BuildSpec{
						CommonSpec: buildapi.CommonSpec{
							NodeSelector: map[string]string{
								"kubernetes.io/arch": "amd64",
							},
						},
					},
				},
			},
		},
		{
			name:              "basic case - multi architectures",
			nodeArchitectures: []string{"amd64", "arm64", "ppc64"},
			build: buildapi.Build{
				ObjectMeta: meta.ObjectMeta{Name: "test-build"},
			},
			want: []buildapi.Build{
				{
					ObjectMeta: meta.ObjectMeta{Name: "test-build"},
					Spec: buildapi.BuildSpec{
						CommonSpec: buildapi.CommonSpec{
							NodeSelector: map[string]string{
								"kubernetes.io/arch": "amd64",
							},
						},
					},
				},
				{
					ObjectMeta: meta.ObjectMeta{Name: "test-build-arm64"},
					Spec: buildapi.BuildSpec{
						CommonSpec: buildapi.CommonSpec{
							NodeSelector: map[string]string{
								"kubernetes.io/arch": "arm64",
							},
						},
					},
				},
				{
					ObjectMeta: meta.ObjectMeta{Name: "test-build-ppc64"},
					Spec: buildapi.BuildSpec{
						CommonSpec: buildapi.CommonSpec{
							NodeSelector: map[string]string{
								"kubernetes.io/arch": "ppc64",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(constructMultiArchBuilds(tt.build, tt.nodeArchitectures), tt.want); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
