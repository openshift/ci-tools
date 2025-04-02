package rehearse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const testingRegistry = "../../test/multistage-registry/registry"

// configFiles contains the info needed to allow inlineCiOpConfig to successfully inline
// CONFIG_SPEC and not fail
func generateTestConfigFiles() config.DataByFilename {
	return config.DataByFilename{
		"targetOrg-targetRepo-master.yaml": config.DataWithInfo{
			Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{
					{
						As: "job1",
						MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
							Pre: []api.LiteralTestStep{{FromImage: &api.ImageStreamTagReference{Namespace: "fancy", Name: "willem", Tag: "first"}}},
						},
					},
					{As: "job2"},
				},
			},
			Info: config.Info{
				Metadata: api.Metadata{
					Org:    "targetOrg",
					Repo:   "targetRepo",
					Branch: "master",
				},
			},
		},
		"targetOrg-targetRepo-not-master.yaml": config.DataWithInfo{
			Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{
					{As: "job1"},
					{As: "job2"},
				},
			},
			Info: config.Info{
				Metadata: api.Metadata{
					Org:    "targetOrg",
					Repo:   "targetRepo",
					Branch: "not-master",
				},
			},
		}, "anotherOrg-anotherRepo-master.yaml": config.DataWithInfo{
			Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{
					{As: "job1"},
					{As: "job2"},
				},
			},
			Info: config.Info{
				Metadata: api.Metadata{
					Org:    "anotherOrg",
					Repo:   "anotherRepo",
					Branch: "master",
				},
			},
		},
	}
}

var ignoreUnexported = cmpopts.IgnoreUnexported(prowconfig.Presubmit{}, prowconfig.Brancher{}, prowconfig.RegexpChangeMatcher{}, prowconfig.Periodic{})

type fakeConfigUploader struct {
	baseDir string
}

func (u *fakeConfigUploader) UploadConfigSpec(ctx context.Context, location, ciOpConfigContent string) (string, error) {
	parts := strings.Split(location, "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("can't extract job name from %s", location)
	}
	jobName := parts[len(parts)-1]
	filename := path.Join(u.baseDir, fmt.Sprintf("%s.yaml", jobName))
	err := os.WriteFile(filename, []byte(ciOpConfigContent), 0666)
	if err != nil {
		return "", err
	}
	return filename, nil
}

func TestInlineCiopConfig(t *testing.T) {
	nodeArchitectureAMD64 := api.NodeArchitectureAMD64
	unresolvedConfig := api.ReleaseBuildConfiguration{
		Tests: []api.TestStepConfiguration{
			{
				As: "test1",
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					Pre: []api.TestStep{{LiteralTestStep: &api.LiteralTestStep{
						As:       "test1-from-unresolved",
						From:     "installer",
						Commands: "openshift-cluster install",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						},
						NodeArchitecture: &nodeArchitectureAMD64,
					}}},
				},
			},
			{
				As: "test2",
			},
		},
	}
	unresolvedConfigContent, err := yaml.Marshal(&unresolvedConfig)
	if err != nil {
		t.Fatal("Failed to marshal ci-operator config")
	}
	test1ConfigFromUnresolved := api.ReleaseBuildConfiguration{
		Tests: []api.TestStepConfiguration{
			{
				As: "test1",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre: []api.LiteralTestStep{{As: "test1-from-unresolved",
						From:     "installer",
						Commands: "openshift-cluster install",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "1000m"},
							Limits:   api.ResourceList{"memory": "2Gi"},
						},
						NodeArchitecture: &nodeArchitectureAMD64}},
				},
			},
		},
	}
	uncompressedTest1ConfigContentFromUnresolved, err := yaml.Marshal(&test1ConfigFromUnresolved)
	if err != nil {
		t.Fatalf("Failed to marshal ci-operator config: %v", err)
	}
	test1ConfigContentFromUnresolved, err := gzip.CompressStringAndBase64(string(uncompressedTest1ConfigContentFromUnresolved))
	if err != nil {
		t.Fatalf("Failed to compress config: %v", err)
	}

	resolvedConfig := api.ReleaseBuildConfiguration{
		Tests: []api.TestStepConfiguration{{
			As: "test1",
			MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
				Pre: []api.LiteralTestStep{{FromImage: &api.ImageStreamTagReference{Namespace: "fancy", Name: "willem", Tag: "first"}, NodeArchitecture: &nodeArchitectureAMD64}},
			},
		}, {
			As: "test2",
		}},
	}

	testCiopConfigTest1 := api.ReleaseBuildConfiguration{Tests: []api.TestStepConfiguration{resolvedConfig.Tests[0]}}
	uncompressedTestCiopConfigContentTest1, err := yaml.Marshal(&testCiopConfigTest1)
	if err != nil {
		t.Fatalf("Failed to marshal ci-operator config: %v", err)
	}
	testCiopConfigContentTest1, err := gzip.CompressStringAndBase64(string(uncompressedTestCiopConfigContentTest1))
	if err != nil {
		t.Fatalf("Failed to compress config: %v", err)
	}

	testCiopConfigTest2 := api.ReleaseBuildConfiguration{Tests: []api.TestStepConfiguration{resolvedConfig.Tests[1]}}
	uncompressedTestCiopConfigContentTest2, err := yaml.Marshal(&testCiopConfigTest2)
	if err != nil {
		t.Fatal("Failed to marshal ci-operator config")
	}
	testCiopConfigContentTest2, err := gzip.CompressStringAndBase64(string(uncompressedTestCiopConfigContentTest2))
	if err != nil {
		t.Fatalf("Failed to compress config: %v", err)
	}

	standardMetadata := api.Metadata{Org: "targetOrg", Repo: "targetRepo", Branch: "master"}
	incompleteMetadata := api.Metadata{Org: "openshift", Repo: "release"}

	makePresubmit := func(command string, env []v1.EnvVar, args []string) *prowconfig.Presubmit {
		return &prowconfig.Presubmit{
			JobBase: prowconfig.JobBase{
				Agent:  "kubernetes",
				Name:   "test-job-name",
				Labels: map[string]string{"pj-rehearse.openshift.io/can-be-rehearsed": "true"},
				Spec: &v1.PodSpec{
					Containers: []v1.Container{
						{
							Args:    args,
							Command: []string{command},
							Env:     env,
						},
					},
				},
			},
		}
	}

	configs := config.DataByFilename{
		standardMetadata.Basename(): {
			Info: config.Info{
				Metadata: standardMetadata,
			},
			Configuration: resolvedConfig,
		},
	}

	testCases := []struct {
		description string

		testname  string
		command   string
		sourceEnv []v1.EnvVar
		metadata  api.Metadata

		expectedEnv               []v1.EnvVar
		expectedUploadContent     string
		expectedError             bool
		expectedImageStreamTagMap apihelper.ImageStreamTagMap
	}{
		{
			description: "not a ci-operator job -> no changes",
			command:     "not-ci-operator",
			metadata:    standardMetadata,
		},
		{
			description: "ci-operator job with CONFIG_SPEC -> no changes",
			sourceEnv:   []v1.EnvVar{{Name: "CONFIG_SPEC", Value: "this is kept"}},
			metadata:    standardMetadata,
			expectedEnv: []v1.EnvVar{{Name: "CONFIG_SPEC", Value: "this is kept"}},
		},
		{
			description:               "ci-operator job -> adds CONFIG_SPEC with resolved config for the given test (test1)",
			testname:                  "test1",
			metadata:                  standardMetadata,
			expectedUploadContent:     testCiopConfigContentTest1,
			expectedImageStreamTagMap: apihelper.ImageStreamTagMap{"fancy/willem:first": types.NamespacedName{Namespace: "fancy", Name: "willem:first"}},
		},
		{
			description:           "ci-operator job -> adds CONFIG_SPEC with resolved config for the given test (test2)",
			testname:              "test2",
			metadata:              standardMetadata,
			expectedUploadContent: testCiopConfigContentTest2,
		},
		{
			description:           "ci-operator job with UNRESOLVED_CONFIG -> adds CONFIG_SPEC with resolved config for the given test (test1)",
			testname:              "test1",
			metadata:              standardMetadata,
			sourceEnv:             []v1.EnvVar{{Name: "UNRESOLVED_CONFIG", Value: string(unresolvedConfigContent)}},
			expectedUploadContent: test1ConfigContentFromUnresolved,
		},
		{
			description:   "Incomplete metadata -> error",
			testname:      "test1",
			metadata:      incompleteMetadata,
			expectedError: true,
		},
		{
			description: "A non-ci-operator jobs with UNRESOLVED_CONFIG should be left untouched",
			command:     "not-ci-operator",
			metadata:    standardMetadata,
			sourceEnv:   []v1.EnvVar{{Name: "UNRESOLVED_CONFIG", Value: "should not change"}},
			expectedEnv: []v1.EnvVar{{Name: "UNRESOLVED_CONFIG", Value: "should not change"}},
		},
	}

	references, chains, workflows, _, _, _, observers, err := load.Registry(testingRegistry, load.RegistryFlag(0))
	if err != nil {
		t.Fatalf("Failed to read registry: %v", err)
	}
	resolver := registry.NewResolver(references, chains, workflows, observers)
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			logger := logrus.NewEntry(logrus.New())
			uploadDir := t.TempDir()
			if tc.command == "" {
				tc.command = "ci-operator"
			}
			var args []string
			if tc.testname != "" {
				args = append(args, fmt.Sprintf("--target=%s", tc.testname))
			}
			job := makePresubmit(tc.command, tc.sourceEnv, args)
			jobName := fmt.Sprintf("pull-ci-%s", tc.testname)
			if tc.expectedUploadContent != "" {
				tc.expectedEnv = append(tc.expectedEnv, v1.EnvVar{Name: "CONFIG_SPEC_GCS_URL", Value: path.Join(uploadDir, fmt.Sprintf("%s.yaml", jobName))})
			}
			expectedJob := makePresubmit(tc.command, tc.expectedEnv, args)

			uploader := &fakeConfigUploader{baseDir: uploadDir}
			jc := NewJobConfigurer(false, configs, &prowconfig.Config{}, resolver, logger, makeBaseRefs(), uploader)
			imageStreamTags, err := jc.inlineCiOpConfig(&job.Spec.Containers[0], configs, resolver, tc.metadata, tc.testname, jobName, logger)

			if tc.expectedError && err == nil {
				t.Fatalf("Expected inlineCiopConfig() to return an error, none returned")
			}

			if !tc.expectedError {
				if err != nil {
					t.Fatalf("Unexpected error returned by inlineCiOpConfig(): %v", err)
				}

				if diff := cmp.Diff(imageStreamTags, tc.expectedImageStreamTagMap, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("returned imageStreamTags differ from expected: %s", diff)
				}

				if !equality.Semantic.DeepEqual(expectedJob, job) {
					t.Errorf("Returned job differs from expected:\n%s", cmp.Diff(expectedJob, job, ignoreUnexported))
				}

				if tc.expectedUploadContent != "" {
					uploadedCiOpConfig, err := os.ReadFile(path.Join(uploadDir, fmt.Sprintf("%s.yaml", jobName)))
					if err != nil {
						t.Fatalf("Failed to read uploaded ci-operator config file: %v", err)
					}
					compressedConfig, err := gzip.CompressStringAndBase64(string(uploadedCiOpConfig))
					if err != nil {
						t.Fatalf("Failed to compress uploaded ci-operator config file: %v", err)
					}
					if diff := cmp.Diff(tc.expectedUploadContent, compressedConfig); diff != "" {
						t.Errorf("upload ci-operator config content differs from expected:\n%s", diff)
					}
				}
			}
		})
	}
}

func makeTestingPresubmit(name, context, branch string) *prowconfig.Presubmit {
	return &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Agent:  "kubernetes",
			Name:   name,
			Labels: map[string]string{Label: "123", jobconfig.CanBeRehearsedLabel: "true"},
			Spec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
					Args:    []string{"--resolver-address=http://ci-operator-resolver", "--org", "openshift", "--repo=origin", "--branch", "master"},
				}},
			},
		},
		RerunCommand: fmt.Sprintf("/pj-rehearse %s", name),
		Reporter:     prowconfig.Reporter{Context: context},
		Brancher: prowconfig.Brancher{Branches: []string{
			fmt.Sprintf("^%s$", branch),
		}},
	}
}

func TestMakeRehearsalPresubmit(t *testing.T) {
	testRepo := "org/repo"

	sourcePresubmit := &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Agent: "kubernetes",
			Name:  "pull-ci-org-repo-branch-test",
			Spec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
					Args:    []string{"arg1", "arg2"},
				}},
			},
		},
		RerunCommand: "/test test",
		Reporter:     prowconfig.Reporter{Context: "ci/prow/test"},
		Brancher:     prowconfig.Brancher{Branches: []string{"^branch$"}},
	}
	yes := true
	otherPresubmit := &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Agent: "kubernetes",
			Name:  "pull-ci-org-repo-branch-test",
			Spec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
					Args:    []string{"arg1", "arg2"},
				}},
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate:       &yes,
				PathAlias:      "pathalias",
				CloneURI:       "cloneuri",
				SkipSubmodules: true,
				CloneDepth:     10,
				SkipFetchHead:  true,
			},
		},
		RerunCommand: "/test test",
		Reporter:     prowconfig.Reporter{Context: "ci/prow/test"},
		Brancher:     prowconfig.Brancher{Branches: []string{"^branch$"}},
	}
	hiddenPresubmit := &prowconfig.Presubmit{}
	if err := deepcopy.Copy(hiddenPresubmit, sourcePresubmit); err != nil {
		t.Fatalf("deepcopy failed: %v", err)
	}
	hiddenPresubmit.Hidden = true

	reportingPresubmit := &prowconfig.Presubmit{}
	if err := deepcopy.Copy(reportingPresubmit, sourcePresubmit); err != nil {
		t.Fatalf("deepcopy failed: %v", err)
	}
	reportingPresubmit.ReporterConfig = &pjapi.ReporterConfig{Slack: &pjapi.SlackReporterConfig{}}

	testCases := []struct {
		testID   string
		refs     *pjapi.Refs
		original *prowconfig.Presubmit
	}{
		{
			testID:   "job that belong to different org/repo than refs",
			refs:     &pjapi.Refs{Org: "anotherOrg", Repo: "anotherRepo", Pulls: []pjapi.Pull{{Number: 123}}},
			original: sourcePresubmit,
		},
		{
			testID:   "job that belong to different org/repo than refs with custom config",
			refs:     &pjapi.Refs{Org: "anotherOrg", Repo: "anotherRepo", Pulls: []pjapi.Pull{{Number: 123}}},
			original: otherPresubmit,
		},
		{
			testID:   "job that belong to the same org/repo with refs",
			refs:     &pjapi.Refs{Org: "org", Repo: "repo", Pulls: []pjapi.Pull{{Number: 123}}},
			original: sourcePresubmit,
		},
		{
			testID:   "hidden job that belong to the same org/repo with refs",
			refs:     &pjapi.Refs{Org: "org", Repo: "repo", Pulls: []pjapi.Pull{{Number: 123}}},
			original: hiddenPresubmit,
		},
		{
			testID:   "job that belong to the same org but different repo than refs",
			refs:     &pjapi.Refs{Org: "org", Repo: "anotherRepo", Pulls: []pjapi.Pull{{Number: 123}}},
			original: sourcePresubmit,
		},
		{
			testID:   "reporting configuration is stripped from rehearsals to avoid polluting",
			refs:     &pjapi.Refs{Org: "anotherOrg", Repo: "anotherRepo", Pulls: []pjapi.Pull{{Number: 123}}},
			original: reportingPresubmit,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			rehearsal, err := makeRehearsalPresubmit(tc.original, testRepo, tc.refs)
			if err != nil {
				t.Fatalf("failed to make rehearsal presubmit: %v", err)
			}
			serializedResult, err := yaml.Marshal(rehearsal)
			if err != nil {
				t.Fatalf("failed to serialize job: %v", err)
			}
			testhelper.CompareWithFixture(t, string(serializedResult))
		})
	}
}

func makeTestingProwJob(namespace, rehearseJobName, context, testName string, refs *pjapi.Refs, org, repo, branch, baseDir, jobURLPrefix string) *pjapi.ProwJob {
	return &pjapi.ProwJob{
		TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "generatedTestName",
			Namespace: namespace,
			Labels: map[string]string{
				"created-by-prow":       "true",
				"prow.k8s.io/job":       rehearseJobName,
				"prow.k8s.io/refs.org":  refs.Org,
				"prow.k8s.io/refs.repo": refs.Repo,
				"prow.k8s.io/type":      "presubmit",
				"prow.k8s.io/refs.pull": strconv.Itoa(refs.Pulls[0].Number),
				Label:                   strconv.Itoa(refs.Pulls[0].Number),
			},
			Annotations: map[string]string{"prow.k8s.io/job": rehearseJobName},
		},
		Spec: pjapi.ProwJobSpec{
			Agent:        "kubernetes",
			Type:         pjapi.PresubmitJob,
			Job:          rehearseJobName,
			Refs:         refs,
			Report:       true,
			Context:      context,
			RerunCommand: fmt.Sprintf("/pj-rehearse %s", testName),
			ExtraRefs: []pjapi.Refs{
				{
					Org:     org,
					Repo:    repo,
					BaseRef: branch,
					WorkDir: true,
				},
			},
			PodSpec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
					Args:    []string{},
					Env:     []v1.EnvVar{{Name: "CONFIG_SPEC_GCS_URL", Value: fmt.Sprintf("%s/%s.yaml", baseDir, testName)}},
				}},
			},
			DecorationConfig: &pjapi.DecorationConfig{
				GCSConfiguration: &pjapi.GCSConfiguration{
					JobURLPrefix: jobURLPrefix,
				},
			},
		},
		Status: pjapi.ProwJobStatus{
			State: pjapi.TriggeredState,
		},
	}
}

func makeTestData() (testNamespace string, testReleasePath string, testRefs *pjapi.Refs) {
	testNamespace = "test-namespace"
	testRefs = &pjapi.Refs{
		Org:     "testOrg",
		Repo:    "testRepo",
		BaseRef: "testBaseRef",
		BaseSHA: "testBaseSHA",
		Pulls:   []pjapi.Pull{{Number: 123, Author: "testAuthor", SHA: "testPrSHA"}},
	}
	testReleasePath = "path/to/openshift/release"
	return
}

func setSuccessCreateReactor(in runtime.Object) error {
	pj := in.(*pjapi.ProwJob)
	pj.Status.State = pjapi.SuccessState
	return nil
}

func TestExecuteJobsErrors(t *testing.T) {
	testNamespace, testRepoPath, testRefs := makeTestData()
	targetOrgRepo := "targetOrg/targetRepo"
	testCiopConfigs := generateTestConfigFiles()

	testCases := []struct {
		description  string
		jobs         map[string][]prowconfig.Presubmit
		failToCreate sets.Set[string]
	}{{
		description: "fail to Create a prowjob",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", "master"),
		}},
		failToCreate: sets.New[string]("rehearse-123-job1"),
	}, {
		description: "fail to Create one of two prowjobs",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", "master"),
			*makeTestingPresubmit("job2", "ci/prow/job2", "master"),
		}},
		failToCreate: sets.New[string]("rehearse-123-job2"),
	}}

	references, chains, workflows, _, _, _, observers, err := load.Registry(testingRegistry, load.RegistryFlag(0))
	if err != nil {
		t.Fatalf("Failed to read registry: %v", err)
	}
	resolver := registry.NewResolver(references, chains, workflows, observers)
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			logger := logrus.NewEntry(logrus.New())
			client := newTC()
			client.createReactors = append(client.createReactors,
				func(in runtime.Object) error {
					pj := in.(*pjapi.ProwJob)
					if tc.failToCreate.Has(pj.Spec.Job) {
						return errors.New("fail")
					}
					return nil
				},
				setSuccessCreateReactor,
			)

			uploader := &fakeConfigUploader{baseDir: t.TempDir()}
			jc := NewJobConfigurer(false, testCiopConfigs, &prowconfig.Config{}, resolver, logger, makeBaseRefs(), uploader)

			_, presubmits, err := jc.ConfigurePresubmitRehearsals(tc.jobs)
			if err != nil {
				t.Errorf("Expected to get no error, but got one: %v", err)
			}
			executor := NewExecutor(presubmits, testRepoPath, testRefs, true, logger, client, testNamespace, &prowconfig.Config{}, true)
			executor.pollFunc = threetimesTryingPoller
			_, err = executor.ExecuteJobs()

			if err == nil {
				t.Errorf("Expected to return error, got nil")
			}
		})
	}
}

func TestExecuteJobsUnsuccessful(t *testing.T) {
	testNamespace, testRepoPath, testRefs := makeTestData()
	targetOrgRepo := "targetOrg/targetRepo"
	testCiopConfigs := generateTestConfigFiles()

	testCases := []struct {
		description string
		jobs        map[string][]prowconfig.Presubmit
		results     map[string]pjapi.ProwJobState
	}{{
		description: "single job that fails",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", "master"),
		}},
		results: map[string]pjapi.ProwJobState{"rehearse-123-job1": pjapi.FailureState},
	}, {
		description: "single job that aborts",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", "master"),
		}},
		results: map[string]pjapi.ProwJobState{"rehearse-123-job1": pjapi.AbortedState},
	}, {
		description: "one job succeeds, one fails",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", "master"),
			*makeTestingPresubmit("job2", "ci/prow/job2", "master"),
		}},
		results: map[string]pjapi.ProwJobState{
			"rehearse-123-job1": pjapi.SuccessState,
			"rehearse-123-job2": pjapi.FailureState,
		},
	}}

	references, chains, workflows, _, _, _, observers, err := load.Registry(testingRegistry, load.RegistryFlag(0))
	if err != nil {
		t.Fatalf("Failed to read registry: %v", err)
	}
	resolver := registry.NewResolver(references, chains, workflows, observers)
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			logger := logrus.NewEntry(logrus.New())
			client := newTC()
			client.createReactors = append(client.createReactors,
				func(in runtime.Object) error {
					pj := in.(*pjapi.ProwJob)
					pj.Status.State = tc.results[pj.Spec.Job]
					return nil
				},
			)

			uploader := &fakeConfigUploader{baseDir: t.TempDir()}
			jc := NewJobConfigurer(false, testCiopConfigs, &prowconfig.Config{}, resolver, logger, makeBaseRefs(), uploader)
			_, presubmits, err := jc.ConfigurePresubmitRehearsals(tc.jobs)
			if err != nil {
				t.Errorf("Expected to get no error, but got one: %v", err)
			}
			executor := NewExecutor(presubmits, testRepoPath, testRefs, false, logger, client, testNamespace, &prowconfig.Config{}, true)
			executor.pollFunc = threetimesTryingPoller
			success, _ := executor.ExecuteJobs()

			if success {
				t.Errorf("Expected to return success=false, got true")
			}
		})
	}
}

func TestExecuteJobsPositive(t *testing.T) {
	testNamespace, testRepoPath, testRefs := makeTestData()
	rehearseJobContextTemplate := "ci/rehearse/%s/%s/%s"
	targetOrgRepo := "targetOrg/targetRepo"
	anotherTargetOrgRepo := "anotherOrg/anotherRepo"
	targetOrg := "targetOrg"
	targetRepo := "targetRepo"
	anotherTargetOrg := "anotherOrg"
	anotherTargetRepo := "anotherRepo"
	testCiopConfigs := generateTestConfigFiles()
	targetOrgRepoPrefix := "https://org.repo.com/"
	baseDir := t.TempDir()

	testCases := []struct {
		description               string
		jobs                      map[string][]prowconfig.Presubmit
		expectedJobs              []pjapi.ProwJobSpec
		expectedImageStreamTagMap apihelper.ImageStreamTagMap
	}{
		{
			description: "two jobs in a single repo",
			jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
				*makeTestingPresubmit("job1", "ci/prow/job1", "master"),
				*makeTestingPresubmit("job2", "ci/prow/job2", "master"),
			}},
			expectedJobs: []pjapi.ProwJobSpec{
				makeTestingProwJob(testNamespace,
					"rehearse-123-job1",
					fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "master", "job1"),
					"job1", testRefs, targetOrg, targetRepo, "master", baseDir, targetOrgRepoPrefix).Spec,
				makeTestingProwJob(testNamespace,
					"rehearse-123-job2",
					fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "master", "job2"),
					"job2", testRefs, targetOrg, targetRepo, "master", baseDir, targetOrgRepoPrefix).Spec,
			},
			expectedImageStreamTagMap: apihelper.ImageStreamTagMap{"fancy/willem:first": types.NamespacedName{Namespace: "fancy", Name: "willem:first"}},
		}, {
			description: "two jobs in a single repo, same context but different branch",
			jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
				*makeTestingPresubmit("job1", "ci/prow/job1", "master"),
				*makeTestingPresubmit("job2", "ci/prow/job2", "not-master"),
			}},
			expectedJobs: []pjapi.ProwJobSpec{
				makeTestingProwJob(testNamespace,
					"rehearse-123-job1",
					fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "master", "job1"),
					"job1", testRefs, targetOrg, targetRepo, "master", baseDir, targetOrgRepoPrefix).Spec,
				makeTestingProwJob(testNamespace,
					"rehearse-123-job2",
					fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "not-master", "job2"),
					"job2", testRefs, targetOrg, targetRepo, "not-master", baseDir, targetOrgRepoPrefix).Spec,
			},
			expectedImageStreamTagMap: apihelper.ImageStreamTagMap{"fancy/willem:first": types.NamespacedName{Namespace: "fancy", Name: "willem:first"}},
		},
		{
			description: "two jobs in a separate repos",
			jobs: map[string][]prowconfig.Presubmit{
				targetOrgRepo:        {*makeTestingPresubmit("job1", "ci/prow/job1", "master")},
				anotherTargetOrgRepo: {*makeTestingPresubmit("job2", "ci/prow/job2", "master")},
			},
			expectedJobs: []pjapi.ProwJobSpec{
				makeTestingProwJob(testNamespace,
					"rehearse-123-job1",
					fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "master", "job1"),
					"job1", testRefs, targetOrg, targetRepo, "master", baseDir, targetOrgRepoPrefix).Spec,
				makeTestingProwJob(testNamespace,
					"rehearse-123-job2",
					fmt.Sprintf(rehearseJobContextTemplate, anotherTargetOrgRepo, "master", "job2"),
					"job2", testRefs, anotherTargetOrg, anotherTargetRepo, "master", baseDir, "https://star.com/").Spec,
			},
			expectedImageStreamTagMap: apihelper.ImageStreamTagMap{"fancy/willem:first": types.NamespacedName{Namespace: "fancy", Name: "willem:first"}},
		}, {
			description:  "no jobs",
			jobs:         map[string][]prowconfig.Presubmit{},
			expectedJobs: []pjapi.ProwJobSpec{},
		},
	}

	references, chains, workflows, _, _, _, observers, err := load.Registry(testingRegistry, load.RegistryFlag(0))
	if err != nil {
		t.Fatalf("Failed to read registry: %v", err)
	}
	resolver := registry.NewResolver(references, chains, workflows, observers)
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			logger := logrus.NewEntry(logrus.New())
			client := newTC()
			client.createReactors = append(client.createReactors, setSuccessCreateReactor)

			pc := prowconfig.Config{
				ProwConfig: prowconfig.ProwConfig{
					Plank: prowconfig.Plank{
						JobURLPrefixConfig: map[string]string{
							"*":           "https://star.com/",
							targetOrg:     "https://org.com/",
							targetOrgRepo: targetOrgRepoPrefix,
						}},
				}}
			uploader := &fakeConfigUploader{baseDir: baseDir}
			jc := NewJobConfigurer(false, testCiopConfigs, &pc, resolver, logger, makeBaseRefs(), uploader)
			imageStreamTags, presubmits, err := jc.ConfigurePresubmitRehearsals(tc.jobs)
			if err != nil {
				t.Errorf("Expected to get no error, but got one: %v", err)
			}
			if diff := cmp.Diff(imageStreamTags, tc.expectedImageStreamTagMap, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("returned imageStreamTags do not match expected: %s", diff)
			}
			executor := NewExecutor(presubmits, testRepoPath, testRefs, true, logger, client, testNamespace, &prowconfig.Config{}, true)
			success, err := executor.ExecuteJobs()

			if err != nil {
				t.Errorf("Expected ExecuteJobs() to not return error, returned %v", err)
				return
			}

			if !success {
				t.Errorf("Expected ExecuteJobs() to return success=true, got false")
			}

			createdJobs := &pjapi.ProwJobList{}
			if err := client.List(context.Background(), createdJobs); err != nil {
				t.Fatalf("failed to list prowjobs from client: %v", err)
			}

			var createdJobSpecs []pjapi.ProwJobSpec
			for _, job := range createdJobs.Items {
				createdJobSpecs = append(createdJobSpecs, job.Spec)
			}

			// Sort to allow comparison
			sort.Slice(tc.expectedJobs, func(a, b int) bool { return tc.expectedJobs[a].Job < tc.expectedJobs[b].Job })
			sort.Slice(createdJobSpecs, func(a, b int) bool { return createdJobSpecs[a].Job < createdJobSpecs[b].Job })

			if !equality.Semantic.DeepEqual(tc.expectedJobs, createdJobSpecs) {
				t.Errorf("Created ProwJobs differ from expected:\n%s", cmp.Diff(tc.expectedJobs, createdJobSpecs, ignoreUnexported))
			}
		})
	}
}

func TestWaitForJobs(t *testing.T) {
	logger := logrus.NewEntry(logrus.New())
	pjSuccess0 := pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "success0"},
		Status:     pjapi.ProwJobStatus{State: pjapi.SuccessState},
	}
	pjSuccess1 := pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "success1"},
		Status:     pjapi.ProwJobStatus{State: pjapi.SuccessState},
	}
	pjFailure := pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "failure"},
		Status:     pjapi.ProwJobStatus{State: pjapi.FailureState},
	}
	pjPending := pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "pending"},
		Status:     pjapi.ProwJobStatus{State: pjapi.PendingState},
	}
	pjAborted := pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "aborted"},
		Status:     pjapi.ProwJobStatus{State: pjapi.AbortedState},
	}
	pjTriggered := pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "triggered"},
		Status:     pjapi.ProwJobStatus{State: pjapi.TriggeredState},
	}
	pjError := pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "error"},
		Status:     pjapi.ProwJobStatus{State: pjapi.ErrorState},
	}
	testCases := []struct {
		id      string
		pjs     sets.Set[string]
		events  []runtime.Object
		success bool
		err     error
	}{{
		id:      "empty",
		success: true,
	}, {
		id:      "one successful job",
		success: true,
		pjs:     sets.New[string]("success0"),
		events:  []runtime.Object{&pjSuccess0},
	}, {
		id:  "mixed states",
		pjs: sets.New[string]("failure", "success0", "aborted", "error"),
		events: []runtime.Object{
			&pjFailure, &pjPending, &pjSuccess0,
			&pjTriggered, &pjAborted, &pjError,
		},
	}, {
		id:      "ignored states",
		success: true,
		pjs:     sets.New[string]("success0"),
		events:  []runtime.Object{&pjPending, &pjSuccess0, &pjTriggered},
	}, {
		id:      "not watched",
		success: true,
		pjs:     sets.New[string]("success1"),
		events:  []runtime.Object{&pjSuccess0, &pjFailure, &pjSuccess1},
	}, {
		id:     "not watched failure",
		pjs:    sets.New[string]("failure"),
		events: []runtime.Object{&pjSuccess0, &pjFailure},
	}}
	for idx := range testCases {
		tc := testCases[idx]
		t.Run(tc.id, func(t *testing.T) {
			client := newTC(tc.events...)

			executor := NewExecutor(nil, "", &pjapi.Refs{}, true, logger, client, "", &prowconfig.Config{}, true)
			executor.pollFunc = threetimesTryingPoller
			success, err := executor.waitForJobs(tc.pjs, &ctrlruntimeclient.ListOptions{})
			if err != tc.err {
				t.Fatalf("want `err` == %v, got %v", tc.err, err)
			}
			if success != tc.success {
				t.Fatalf("want `success` == %v, got %v", tc.success, success)
			}
		})
	}
}

func TestWaitForJobsRetries(t *testing.T) {
	client := newTC()
	var try int
	client.postListReactors = append(client.postListReactors, func(in runtime.Object) error {
		if try < 1 {
			try++
		} else {
			pjList := in.(*pjapi.ProwJobList)
			pjList.Items = append(pjList.Items, pjapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "j"},
				Status:     pjapi.ProwJobStatus{State: pjapi.SuccessState},
			})
		}
		return nil
	})

	executor := NewExecutor(nil, "", &pjapi.Refs{}, true, logrus.NewEntry(logrus.New()), client, "", &prowconfig.Config{}, true)
	executor.pollFunc = threetimesTryingPoller
	success, err := executor.waitForJobs(sets.Set[string]{"j": {}}, &ctrlruntimeclient.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !success {
		t.Error("expected success, didn't get it")
	}
}

func TestWaitForJobsLog(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	client := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
		&pjapi.ProwJob{
			ObjectMeta: metav1.ObjectMeta{Name: "success"},
			Status:     pjapi.ProwJobStatus{State: pjapi.SuccessState}},
		&pjapi.ProwJob{
			ObjectMeta: metav1.ObjectMeta{Name: "failure"},
			Status:     pjapi.ProwJobStatus{State: pjapi.FailureState}},
	).Build()

	executor := NewExecutor(nil, "", &pjapi.Refs{}, true, logger.WithFields(nil), client, "", &prowconfig.Config{}, true)
	executor.pollFunc = threetimesTryingPoller
	_, err := executor.waitForJobs(sets.New[string]("success", "failure"), &ctrlruntimeclient.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	check := func(hook *logrustest.Hook, name string, level logrus.Level, state *pjapi.ProwJobState) {
		for _, entry := range hook.Entries {
			if entry.Level == level && entry.Data["name"] == name && (state == nil || entry.Data["state"].(pjapi.ProwJobState) == *state) {
				return
			}
		}
		if state == nil {
			t.Errorf("no log entry with name == %q, level == %q found", name, level)
		} else {
			t.Errorf("no log entry with name == %q, level == %q, and state == %q found", name, level, *state)
		}
	}
	successState, failureState := pjapi.SuccessState, pjapi.FailureState
	check(hook, "success", logrus.InfoLevel, &successState)
	check(hook, "failure", logrus.InfoLevel, &failureState)
}

func TestFilterPresubmits(t *testing.T) {
	canBeRehearsed := map[string]string{"pj-rehearse.openshift.io/can-be-rehearsed": "true"}

	testCases := []struct {
		description   string
		presubmits    config.Presubmits
		disabledNames []string
		expected      config.Presubmits
	}{
		{
			description: "basic presubmit job, allowed",
			presubmits:  config.Presubmits{"org/repo": {*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test")}},
			expected:    config.Presubmits{"org/repo": {*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test")}},
		},
		{
			description: "job with no rehearse label, not allowed",
			presubmits:  config.Presubmits{"org/repo": {*makePresubmit(map[string]string{}, false, "pull-ci-organization-repo-master-test")}},
			expected:    config.Presubmits{},
		},
		{
			description: "hidden job, not allowed",
			presubmits:  config.Presubmits{"org/repo": {*makePresubmit(canBeRehearsed, true, "pull-ci-organization-repo-master-test")}},
			expected:    config.Presubmits{},
		},
		{
			description:   "job in disabled list, not allowed",
			presubmits:    config.Presubmits{"org/repo": {*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test")}},
			disabledNames: []string{"pull-ci-organization-repo-master-test"},
			expected:      config.Presubmits{},
		},
		{
			description: "multiple jobs, some allowed",
			presubmits: config.Presubmits{"org/repo": {
				*makePresubmit(canBeRehearsed, true, "pull-ci-organization-repo-master-test-0"),
				*makePresubmit(map[string]string{}, false, "pull-ci-organization-repo-master-test-1"),
				*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test-2"),
				*makePresubmit(map[string]string{}, false, "pull-ci-organization-repo-master-test-3"),
				*makePresubmit(canBeRehearsed, true, "pull-ci-organization-repo-master-test-4"),
				*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test-5"),
				*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test-6")},
			},
			disabledNames: []string{"pull-ci-organization-repo-master-test-6"},
			expected: config.Presubmits{"org/repo": {
				*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test-2"),
				*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test-5")},
			},
		},
		{
			description: "multiple repos, some jobs allowed",
			presubmits: config.Presubmits{"org/repo": {*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test"), *makePresubmit(map[string]string{}, false, "pull-ci-organization-repo-master-test")},
				"org/different": {*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test")}},
			expected: config.Presubmits{"org/repo": {*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test")},
				"org/different": {*makePresubmit(canBeRehearsed, false, "pull-ci-organization-repo-master-test")}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			presubmits := filterPresubmits(tc.presubmits, tc.disabledNames, logrus.New())
			if diff := cmp.Diff(tc.expected, presubmits, cmp.AllowUnexported(prowconfig.Brancher{}, prowconfig.RegexpChangeMatcher{}, prowconfig.Presubmit{})); diff != "" {
				t.Fatalf("filtered didn't match expected, diff: %s", diff)
			}
		})

	}
}

func makePresubmit(extraLabels map[string]string, hidden bool, name string) *prowconfig.Presubmit {
	labels := make(map[string]string)
	if len(extraLabels) > 0 {
		labels = extraLabels
	}
	labels["ci.openshift.org/rehearse"] = "123"

	return &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Agent:  "kubernetes",
			Name:   name,
			Labels: labels,
			Spec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
					Args:    []string{"arg"},
				}},
			},
			Hidden: hidden,
		},
		RerunCommand: fmt.Sprintf("/pj-rehearse %s", name),
		Reporter:     prowconfig.Reporter{Context: "ci/prow/test"},
		Brancher:     prowconfig.Brancher{Branches: []string{"^master$"}},
	}
}

func TestFilterPeriodics(t *testing.T) {
	canBeRehearsed := map[string]string{"pj-rehearse.openshift.io/can-be-rehearsed": "true"}

	testCases := []struct {
		description   string
		periodics     config.Periodics
		disabledNames []string
		expected      config.Periodics
	}{
		{
			description: "basic periodic job, allowed",
			periodics:   config.Periodics{"periodic-test": *makePeriodic(canBeRehearsed, false)},
			expected:    config.Periodics{"periodic-test": *makePeriodic(canBeRehearsed, false)},
		},
		{
			description: "job with no rehearse label, not allowed",
			periodics:   config.Periodics{"periodic-test": *makePeriodic(map[string]string{}, false)},
			expected:    config.Periodics{},
		},
		{
			description: "hidden job, not allowed",
			periodics:   config.Periodics{"periodic-test": *makePeriodic(canBeRehearsed, true)},
			expected:    config.Periodics{},
		},
		{
			description:   "job in disabled list, not allowed",
			periodics:     config.Periodics{"periodic-test": *makePeriodic(canBeRehearsed, false)},
			disabledNames: []string{"periodic-test"},
			expected:      config.Periodics{},
		},
		{
			description: "multiple repos, some jobs allowed",
			periodics: config.Periodics{"periodic-test": *makePeriodic(canBeRehearsed, false),
				"other-test":    *makePeriodic(map[string]string{}, false),
				"disabled-test": *makePeriodic(canBeRehearsed, false)},
			disabledNames: []string{"disabled-test"},
			expected:      config.Periodics{"periodic-test": *makePeriodic(canBeRehearsed, false)},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			periodics := filterPeriodics(tc.periodics, tc.disabledNames, logrus.New())
			if diff := cmp.Diff(tc.expected, periodics, cmp.AllowUnexported(prowconfig.Brancher{}, prowconfig.RegexpChangeMatcher{}, prowconfig.Periodic{})); diff != "" {
				t.Fatalf("filtered didn't match expected, diff: %s", diff)
			}
		})

	}
}

func makePeriodic(extraLabels map[string]string, hidden bool) *prowconfig.Periodic {
	labels := make(map[string]string)
	if len(extraLabels) > 0 {
		labels = extraLabels
	}
	labels["ci.openshift.org/rehearse"] = "123"

	return &prowconfig.Periodic{
		JobBase: prowconfig.JobBase{
			Agent:  "kubernetes",
			Name:   "periodic-test",
			Labels: labels,
			Spec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
					Args:    []string{"arg"},
				}},
			},
			Hidden: hidden,
		},
		Cron: "0 * * * *",
	}
}

func TestReplaceConfigMaps(t *testing.T) {
	replacedConfigMaps := map[string]string{
		"changed-template":        "rehearse-template-test-template-00000000",
		"changed-cluster-profile": "rehearse-cluster-profile-test-cp-00000000",
	}

	testCases := []struct {
		description string
		jobVolumes  []v1.Volume
		expected    []v1.Volume
	}{
		{
			description: "no volumes",
			jobVolumes:  []v1.Volume{},
			expected:    []v1.Volume{},
		},
		{
			description: "replace a configmap name in configmap-backed volume",
			jobVolumes:  []v1.Volume{cmVolume("volume-name", "changed-template")},
			expected:    []v1.Volume{cmVolume("volume-name", "rehearse-template-test-template-00000000")},
		},
		{
			description: "replace a configmap name in projected configmap-backed volume",
			jobVolumes:  []v1.Volume{projectedCmVolume("volume-name", "changed-template")},
			expected:    []v1.Volume{projectedCmVolume("volume-name", "rehearse-template-test-template-00000000")},
		},
		{
			description: "do not replace a configmap name in configmap-backed volume",
			jobVolumes:  []v1.Volume{cmVolume("volume-name", "unchanged-template")},
			expected:    []v1.Volume{cmVolume("volume-name", "unchanged-template")},
		},
		{
			description: "do not replace a configmap name in projected configmap-backed volume",
			jobVolumes:  []v1.Volume{projectedCmVolume("volume-name", "unchanged-template")},
			expected:    []v1.Volume{projectedCmVolume("volume-name", "unchanged-template")},
		},
		{
			description: "replace multiple configmap names in many volumes",
			jobVolumes: []v1.Volume{
				cmVolume("first-volume", "changed-template"),
				projectedCmVolume("second-volume", "unchanged-cluster-profile"),
				projectedCmVolume("third-volume", "irrelevant-configmap"),
				cmVolume("fourth-volume", "another-irrelevant-template"),
				projectedCmVolume("fifth-volume", "changed-cluster-profile"),
			},
			expected: []v1.Volume{
				cmVolume("first-volume", "rehearse-template-test-template-00000000"),
				projectedCmVolume("second-volume", "unchanged-cluster-profile"),
				projectedCmVolume("third-volume", "irrelevant-configmap"),
				cmVolume("fourth-volume", "another-irrelevant-template"),
				projectedCmVolume("fifth-volume", "rehearse-cluster-profile-test-cp-00000000"),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			logger := logrus.WithField("testId", testCase.description)
			replaceConfigMaps(testCase.jobVolumes, replacedConfigMaps, logger)
			if !reflect.DeepEqual(testCase.expected, testCase.jobVolumes) {
				t.Fatalf("Volumes differ:\n%v", cmp.Diff(testCase.expected, testCase.jobVolumes))
			}
		})

	}
}

func cmVolume(name, cmName string) v1.Volume {
	return v1.Volume{
		Name: name,
		VolumeSource: v1.VolumeSource{
			ConfigMap: &v1.ConfigMapVolumeSource{
				LocalObjectReference: v1.LocalObjectReference{Name: cmName},
			},
		},
	}
}

func projectedCmVolume(name, cmName string) v1.Volume {
	return v1.Volume{
		Name: name,
		VolumeSource: v1.VolumeSource{
			Projected: &v1.ProjectedVolumeSource{
				Sources: []v1.VolumeProjection{
					{
						ConfigMap: &v1.ConfigMapProjection{
							LocalObjectReference: v1.LocalObjectReference{Name: cmName},
						},
					},
				},
			},
		},
	}
}

func TestGetClusterTypes(t *testing.T) {
	makeJob := func(clusterType string) prowconfig.Presubmit {
		ret := prowconfig.Presubmit{
			JobBase: prowconfig.JobBase{
				Agent: string(pjapi.KubernetesAgent),
			},
		}
		if clusterType != "" {
			ret.Spec = &v1.PodSpec{
				Containers: []v1.Container{{
					Env: []v1.EnvVar{{
						Name:  clusterTypeEnvName,
						Value: clusterType,
					}},
				}},
			}
		}
		return ret
	}
	type Jobs map[string][]prowconfig.Presubmit
	for _, tc := range []struct {
		id   string
		jobs Jobs
		want []string
	}{{
		id:   "no types",
		jobs: Jobs{"org/repo": {makeJob("")}},
	}, {
		id:   "one type",
		jobs: Jobs{"org/repo": {makeJob(""), makeJob("aws")}},
		want: []string{"aws"},
	}, {
		id: "multiple types",
		jobs: Jobs{
			"org/repo":   {makeJob(""), makeJob("aws")},
			"org/sitory": {makeJob("azure"), makeJob("vsphere")},
		},
		want: []string{"aws", "azure", "vsphere"},
	}} {
		t.Run(tc.id, func(t *testing.T) {
			ret := getClusterTypes(tc.jobs)
			if !reflect.DeepEqual(tc.want, ret) {
				t.Fatal(cmp.Diff(tc.want, ret))
			}
		})
	}
}

func makeBaseRefs() *pjapi.Refs {
	return &pjapi.Refs{
		Org:      "openshift",
		Repo:     "release",
		RepoLink: "https://github.com/openshift/release",
		BaseRef:  "master",
		BaseSHA:  "80af9fee7a9f63a79e01da0c74d9dd323118daf0",
		BaseLink: "",
		Pulls: []pjapi.Pull{
			{
				Number: 123,
				Author: "droslean",
				SHA:    "bc825725cfe0acebb06a7e0b11c8228f5a3b89c0",
			},
		},
	}
}

func TestRemoveConfigResolverFlags(t *testing.T) {
	var testCases = []struct {
		description  string
		input        []string
		expectedArgs []string
		expectedInfo api.Metadata
	}{{
		description:  "just resolver flags",
		input:        []string{"--resolver-address=http://ci-operator-resolver", "--org=openshift", "--repo=origin", "--branch=master", "--variant=v2"},
		expectedArgs: nil,
		expectedInfo: api.Metadata{Org: "openshift", Repo: "origin", Branch: "master", Variant: "v2"},
	}, {
		description:  "no resolver flags",
		input:        []string{"--target=target"},
		expectedArgs: []string{"--target=target"},
	}, {
		description:  "mixed resolver and non-resolver flags",
		input:        []string{"--resolver-address=http://ci-operator-resolver", "--org=openshift", "--target=target", "--repo=origin", "--branch=master", "--variant=v2"},
		expectedArgs: []string{"--target=target"},
		expectedInfo: api.Metadata{Org: "openshift", Repo: "origin", Branch: "master", Variant: "v2"},
	}, {
		description:  "spaces in between flag and value",
		input:        []string{"--resolver-address=http://ci-operator-resolver", "--org", "openshift", "--target=target", "--repo", "origin", "--branch", "master", "--variant=v2"},
		expectedArgs: []string{"--target=target"},
		expectedInfo: api.Metadata{Org: "openshift", Repo: "origin", Branch: "master", Variant: "v2"},
	}, {
		description:  "reporting flags",
		input:        []string{"--report-password-file=/etc/report/password.txt", "--report-username=ci", "--resolver-address=http://ci-operator-resolver", "--org", "openshift", "--target=target", "--repo", "origin", "--branch", "master", "--variant=v2"},
		expectedArgs: []string{"--report-password-file=/etc/report/password.txt", "--report-username=ci", "--target=target"},
		expectedInfo: api.Metadata{Org: "openshift", Repo: "origin", Branch: "master", Variant: "v2"},
	}}
	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			newArgs, info := removeConfigResolverFlags(testCase.input)
			if !reflect.DeepEqual(testCase.expectedArgs, newArgs) {
				t.Fatalf("Args differ from expected: %v", cmp.Diff(testCase.expectedArgs, newArgs))
			}
			if !reflect.DeepEqual(testCase.expectedInfo, info) {
				t.Fatalf("ci-operator config info differs from expected: %v", cmp.Diff(testCase.expectedInfo, info))
			}
		})
	}
}

func TestGetTrimmedBranch(t *testing.T) {
	testCases := []struct {
		name     string
		input    []string
		expected string
	}{{
		name:     "master with regex",
		input:    []string{"^master$"},
		expected: "master",
	}, {
		name:     "release-3.5 with regex",
		input:    []string{"^release-3\\.5$"},
		expected: "release-3.5",
	}, {
		name:     "release-4.2 no regex",
		input:    []string{"release-4.2"},
		expected: "release-4.2",
	}}
	for _, testCase := range testCases {
		branch := BranchFromRegexes(testCase.input)
		if branch != testCase.expected {
			t.Errorf("%s: getTrimmedBranches returned %s, expected %s", testCase.name, branch, testCase.expected)
		}
	}
}

func TestVariantFromLabels(t *testing.T) {
	testCases := []struct {
		name     string
		input    map[string]string
		expected string
	}{{
		name:     "no labels",
		input:    map[string]string{},
		expected: "",
	}, {
		name: "unrelated label",
		input: map[string]string{
			"unrelated-label": "true",
		},
		expected: "",
	}, {
		name: "generated and variant labels",
		input: map[string]string{
			"unrelated-label":             "true",
			jobconfig.ProwJobLabelVariant: "v2",
		},
		expected: "v2",
	}}
	for _, testCase := range testCases {
		variant := VariantFromLabels(testCase.input)
		if variant != testCase.expected {
			t.Errorf("%s: VariantFromLabels returned %s, expected %s", testCase.name, variant, testCase.expected)
		}
	}
}

func newTC(initObjs ...runtime.Object) *tc {
	return &tc{Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(initObjs...).Build()}
}

type tc struct {
	ctrlruntimeclient.Client
	createReactors   []func(runtime.Object) error
	postListReactors []func(runtime.Object) error
}

func (tc *tc) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	for _, createReactor := range tc.createReactors {
		if err := createReactor(obj); err != nil {
			return err
		}
	}

	return tc.Client.Create(ctx, obj, opts...)
}

func (tc *tc) List(ctx context.Context, obj ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
	if err := tc.Client.List(ctx, obj, opts...); err != nil {
		return err
	}
	for _, listReactor := range tc.postListReactors {
		if err := listReactor(obj); err != nil {
			return err
		}
	}
	return nil
}

func threetimesTryingPoller(ctx context.Context, _, _ time.Duration, immediate bool, cf wait.ConditionWithContextFunc) error {
	for i := 0; i < 3; i++ {
		success, err := cf(ctx)
		if err != nil {
			return err
		}
		if success {
			return nil
		}
	}
	return wait.ErrorInterrupted(fmt.Errorf("polling failed"))
}

func TestUsesConfigMap(t *testing.T) {
	cmName := "config-map"

	testCases := []struct {
		description string
		volumes     []v1.Volume
		expected    bool
	}{
		{
			description: "no volumes",
		},
		{
			description: "used in projected volume",
			volumes:     []v1.Volume{projectedCmVolume("volume", cmName)},
			expected:    true,
		},
		{
			description: "used directly",
			volumes:     []v1.Volume{cmVolume("volume", cmName)},
			expected:    true,
		},
		{
			description: "not used by any volume",
			volumes: []v1.Volume{
				cmVolume("volume-1", "not-this-cm"),
				projectedCmVolume("volume-2", "neither-this-cm"),
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			job := prowconfig.JobBase{
				Spec: &v1.PodSpec{
					Volumes: append([]v1.Volume{}, tc.volumes...),
				},
			}
			if uses := UsesConfigMap(job, cmName); uses != tc.expected {
				t.Errorf("%s: expected %t, got %t", tc.description, tc.expected, uses)
			}
		})
	}
}

func TestContextFor(t *testing.T) {
	var testCases = []struct {
		name   string
		input  *prowconfig.Presubmit
		output string
	}{
		{
			name:   "presubmit without prefix",
			input:  &prowconfig.Presubmit{Reporter: prowconfig.Reporter{Context: "something"}},
			output: "something",
		},
		{
			name:   "presubmit with prowgen prefix",
			input:  &prowconfig.Presubmit{Reporter: prowconfig.Reporter{Context: "ci/prow/something"}},
			output: "something",
		},
		{
			name:   "presubmit with custom prefix",
			input:  &prowconfig.Presubmit{Reporter: prowconfig.Reporter{Context: "ci/something"}},
			output: "something",
		},
		{
			name:   "periodic",
			input:  &prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "something"}},
			output: "something",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if diff := cmp.Diff(testCase.output, contextFor(testCase.input)); diff != "" {
				t.Errorf("%s: got incorrect context: %v", testCase.name, diff)
			}
		})
	}
}

func TestDetermineJobURLPrefix(t *testing.T) {
	testCases := []struct {
		name     string
		org      string
		repo     string
		expected string
	}{
		{
			name:     "default",
			org:      "someOrg",
			repo:     "someRepo",
			expected: "https://star.com/",
		},
		{
			name:     "by org",
			org:      "org",
			repo:     "someRepo",
			expected: "https://org.com/",
		},
		{
			name:     "by repo",
			org:      "org",
			repo:     "repo",
			expected: "https://org.repo.com/",
		},
	}
	for _, tc := range testCases {
		plank := prowconfig.Plank{JobURLPrefixConfig: map[string]string{
			"*":        "https://star.com/",
			"org":      "https://org.com/",
			"org/repo": "https://org.repo.com/",
		}}
		t.Run(tc.name, func(t *testing.T) {
			actual := determineJobURLPrefix(plank, tc.org, tc.repo)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Fatalf("url prefix did not match expected, diff: %s", diff)
			}
		})
	}
}

func TestMoreRelevant(t *testing.T) {
	testCases := []struct {
		name     string
		one      *config.DataWithInfo
		two      *config.DataWithInfo
		expected bool
	}{
		{
			name: "same org/repo, branches main and release-4.10",
			one: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-main.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "main",
					},
				},
			},
			two: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-release-4.10.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "release-4.10",
					},
				},
			},
			expected: true,
		},
		{
			name: "different org/repo, branches main and release-4.10",
			one: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-main.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "main",
					},
				},
			},
			two: &config.DataWithInfo{
				Info: config.Info{
					Filename: "anotherOrg-anotherRepo-release-4.10.yaml",
					Metadata: api.Metadata{
						Org:    "anotherOrg",
						Repo:   "anotherRepo",
						Branch: "release-4.10",
					},
				},
			},
			expected: true,
		},
		{
			name: "same org/repo, branches release-4.9 and release-4.10",
			one: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-release-4.9.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "release-4.9",
					},
				},
			},
			two: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-release-4.10.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "release-4.10",
					},
				},
			},
			expected: false,
		},
		{
			name: "different org/repo, branches release-4.9 and release-4.10",
			one: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-release-4.9.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "release-4.9",
					},
				},
			},
			two: &config.DataWithInfo{
				Info: config.Info{
					Filename: "anotherOrg-anotherRepo-release-4.10.yaml",
					Metadata: api.Metadata{
						Org:    "anotherOrg",
						Repo:   "anotherRepo",
						Branch: "release-4.10",
					},
				},
			},
			expected: false,
		},
		{
			name: "same org/repo, branches master and not-master",
			one: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-master.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "master",
					},
				},
			},
			two: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-not-master.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "not-master",
					},
				},
			},
			expected: true,
		},
		{
			name: "same org/repo, branches release-4.1 and release-4.10",
			one: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-release-4.1.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "release-4.1",
					},
				},
			},
			two: &config.DataWithInfo{
				Info: config.Info{
					Filename: "targetOrg-targetRepo-release-4.10.yaml",
					Metadata: api.Metadata{
						Org:    "targetOrg",
						Repo:   "targetRepo",
						Branch: "release-4.10",
					},
				},
			},
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := moreRelevant(tc.one, tc.two)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				not := "not "
				if tc.expected {
					not = ""
				}
				t.Fatalf("expected config one to %sbe more relevant than config two, diff: %s", not, diff)
			}
		})
	}
}

func TestSubmitPresubmit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		presubmit  prowconfig.Presubmit
		prowConfig prowconfig.Config
		namespace  string
		refs       pjapi.Refs
		wantJob    pjapi.ProwJob
	}{
		{
			name:       "Spawn a presubmit",
			presubmit:  *makePresubmit(nil, false, "foo"),
			prowConfig: prowconfig.Config{},
			namespace:  "foo-ns",
			refs:       *makeBaseRefs(),
			wantJob: pjapi.ProwJob{
				Spec: pjapi.ProwJobSpec{
					Agent:        "kubernetes",
					Type:         pjapi.PresubmitJob,
					Job:          "foo",
					Refs:         makeBaseRefs(),
					Report:       true,
					Context:      "ci/prow/test",
					RerunCommand: "/pj-rehearse foo",
					PodSpec: &v1.PodSpec{
						Containers: []v1.Container{{
							Command: []string{"ci-operator"},
							Args:    []string{"arg"},
						}},
					},
				},
				Status: pjapi.ProwJobStatus{
					State: pjapi.TriggeredState,
				},
			},
		},
		{
			name:       "Spawn a presubmit in scheduling state",
			presubmit:  *makePresubmit(nil, false, "foo"),
			prowConfig: prowconfig.Config{ProwConfig: prowconfig.ProwConfig{Scheduler: prowconfig.Scheduler{Enabled: true}}},
			namespace:  "foo-ns",
			refs:       *makeBaseRefs(),
			wantJob: pjapi.ProwJob{
				Spec: pjapi.ProwJobSpec{
					Agent:        "kubernetes",
					Type:         pjapi.PresubmitJob,
					Job:          "foo",
					Refs:         makeBaseRefs(),
					Report:       true,
					Context:      "ci/prow/test",
					RerunCommand: "/pj-rehearse foo",
					PodSpec: &v1.PodSpec{
						Containers: []v1.Container{{
							Command: []string{"ci-operator"},
							Args:    []string{"arg"},
						}},
					},
				},
				Status: pjapi.ProwJobStatus{
					State: pjapi.SchedulingState,
				},
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := newTC()
			logger := logrus.NewEntry(logrus.New())
			extor := NewExecutor([]*prowconfig.Presubmit{}, "", &tc.refs, false, logger, client, tc.namespace, &tc.prowConfig, true)
			actualJob, err := extor.submitPresubmit(&tc.presubmit)

			if err != nil {
				t.Errorf("Enexpected error: %s", err)
				return
			}

			if diff := cmp.Diff(&tc.wantJob, actualJob, cmpopts.IgnoreTypes(metav1.TypeMeta{}, metav1.ObjectMeta{}, metav1.Time{})); diff != "" {
				t.Errorf("Enexpected job: %s", diff)
			}
		})
	}
}
