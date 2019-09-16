package rehearse

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"k8s.io/api/core/v1"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/client/clientset/versioned/fake"
	prowconfig "k8s.io/test-infra/prow/config"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"

	clientgoTesting "k8s.io/client-go/testing"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

func TestReplaceClusterProfiles(t *testing.T) {
	makeVolume := func(name string) v1.Volume {
		return v1.Volume{
			Name: "cluster-profile",
			VolumeSource: v1.VolumeSource{
				Projected: &v1.ProjectedVolumeSource{
					Sources: []v1.VolumeProjection{{
						ConfigMap: &v1.ConfigMapProjection{
							LocalObjectReference: v1.LocalObjectReference{
								Name: name,
							},
						},
					}},
				},
			},
		}
	}
	testCases := []struct {
		id       string
		spec     v1.PodSpec
		expected []string
	}{
		{
			id:   "no-profile",
			spec: v1.PodSpec{Containers: []v1.Container{{}}},
		},
		{
			id: "unchanged-profile",
			spec: v1.PodSpec{
				Containers: []v1.Container{{}},
				Volumes:    []v1.Volume{makeVolume(config.ClusterProfilePrefix + "unchanged")},
			},
			expected: []string{config.ClusterProfilePrefix + "unchanged"},
		},
		{
			id: "changed-profile0",
			spec: v1.PodSpec{
				Containers: []v1.Container{{}},
				Volumes:    []v1.Volume{makeVolume(config.ClusterProfilePrefix + "changed-profile0")},
			},
			expected: []string{"rehearse-cluster-profile-changed-profile0-47f520ef"},
		},
		{
			id: "changed-profile1",
			spec: v1.PodSpec{
				Containers: []v1.Container{{}},
				Volumes:    []v1.Volume{makeVolume(config.ClusterProfilePrefix + "changed-profile1")},
			},
			expected: []string{"rehearse-cluster-profile-changed-profile1-85c62707"},
		},
		{
			id: "changed-profiles in multiple volumes",
			spec: v1.PodSpec{
				Containers: []v1.Container{{}},
				Volumes: []v1.Volume{
					makeVolume(config.ClusterProfilePrefix + "unchanged"),
					makeVolume(config.ClusterProfilePrefix + "changed-profile0"),
					makeVolume(config.ClusterProfilePrefix + "changed-profile1"),
					makeVolume(config.ClusterProfilePrefix + "unchanged"),
				},
			},
			expected: []string{
				"cluster-profile-unchanged",
				"rehearse-cluster-profile-changed-profile0-47f520ef",
				"rehearse-cluster-profile-changed-profile1-85c62707",
				"cluster-profile-unchanged"},
		},
	}

	profiles := []config.ConfigMapSource{{
		SHA:      "47f520ef9c2662fc9a2675f1dd4f02d5082b2776",
		Filename: filepath.Join(config.ClusterProfilesPath, "changed-profile0"),
	}, {
		SHA:      "85c627078710b8beee65d06d0cf157094fc46b03",
		Filename: filepath.Join(config.ClusterProfilesPath, "changed-profile1"),
	}}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {

			logger := logrus.WithField("testId", tc.id)
			replaceClusterProfiles(tc.spec.Volumes, profiles, logger)

			var names []string
			if len(tc.spec.Volumes) > 0 {
				for _, volume := range tc.spec.Volumes {
					names = append(names, volume.VolumeSource.Projected.Sources[0].ConfigMap.Name)
				}
			}

			if !reflect.DeepEqual(tc.expected, names) {
				t.Fatal(diff.ObjectDiff(tc.expected, names))
			}
		})
	}
}

func makeTestingPresubmitForEnv(env []v1.EnvVar) *prowconfig.Presubmit {
	return &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Agent:  "kubernetes",
			Name:   "test-job-name",
			Labels: map[string]string{"pj-rehearse.openshift.io/can-be-rehearsed": "true"},
			Spec: &v1.PodSpec{
				Containers: []v1.Container{
					{Env: env},
				},
			},
		},
	}
}

func makeCMReference(cmName, key string) *v1.EnvVarSource {
	return &v1.EnvVarSource{
		ConfigMapKeyRef: &v1.ConfigMapKeySelector{
			LocalObjectReference: v1.LocalObjectReference{
				Name: cmName,
			},
			Key: key,
		},
	}
}

func TestInlineCiopConfig(t *testing.T) {
	testCiopConfigInfo := config.Info{
		Org:    "org",
		Repo:   "repo",
		Branch: "master",
	}
	testCiopConfig := &api.ReleaseBuildConfiguration{}
	testCiopCongigContent, err := yaml.Marshal(testCiopConfig)
	if err != nil {
		t.Fatal("Failed to marshal ci-operator config")
	}

	testCases := []struct {
		description   string
		sourceEnv     []v1.EnvVar
		configs       config.CompoundCiopConfig
		expectedEnv   []v1.EnvVar
		expectedError bool
	}{{
		description: "empty env -> no changes",
		configs:     config.CompoundCiopConfig{},
	}, {
		description: "no Env.ValueFrom -> no changes",
		sourceEnv:   []v1.EnvVar{{Name: "T", Value: "V"}},
		configs:     config.CompoundCiopConfig{},
		expectedEnv: []v1.EnvVar{{Name: "T", Value: "V"}},
	}, {
		description: "no Env.ValueFrom.ConfigMapKeyRef -> no changes",
		sourceEnv:   []v1.EnvVar{{Name: "T", ValueFrom: &v1.EnvVarSource{ResourceFieldRef: &v1.ResourceFieldSelector{}}}},
		configs:     config.CompoundCiopConfig{},
		expectedEnv: []v1.EnvVar{{Name: "T", ValueFrom: &v1.EnvVarSource{ResourceFieldRef: &v1.ResourceFieldSelector{}}}},
	}, {
		description: "CM reference but not ci-operator-configs -> no changes",
		sourceEnv:   []v1.EnvVar{{Name: "T", ValueFrom: makeCMReference("test-cm", "key")}},
		configs:     config.CompoundCiopConfig{},
		expectedEnv: []v1.EnvVar{{Name: "T", ValueFrom: makeCMReference("test-cm", "key")}},
	}, {
		description: "CM reference to ci-operator-configs -> cm content inlined",
		sourceEnv:   []v1.EnvVar{{Name: "T", ValueFrom: makeCMReference(testCiopConfigInfo.ConfigMapName(), "filename")}},
		configs:     config.CompoundCiopConfig{"filename": testCiopConfig},
		expectedEnv: []v1.EnvVar{{Name: "T", Value: string(testCiopCongigContent)}},
	}, {
		description:   "bad CM key is handled",
		sourceEnv:     []v1.EnvVar{{Name: "T", ValueFrom: makeCMReference(testCiopConfigInfo.ConfigMapName(), "filename")}},
		configs:       config.CompoundCiopConfig{},
		expectedError: true,
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			testLoggers := Loggers{logrus.New(), logrus.New()}
			job := makeTestingPresubmitForEnv(tc.sourceEnv)
			expectedJob := makeTestingPresubmitForEnv(tc.expectedEnv)

			err := inlineCiOpConfig(job.Spec.Containers[0], tc.configs, testLoggers)

			if tc.expectedError && err == nil {
				t.Errorf("Expected inlineCiopConfig() to return an error, none returned")
				return
			}

			if !tc.expectedError {
				if err != nil {
					t.Errorf("Unexpected error returned by inlineCiOpConfig(): %v", err)
					return
				}

				if !equality.Semantic.DeepEqual(expectedJob, job) {
					t.Errorf("Returned job differs from expected:\n%s", diff.ObjectReflectDiff(expectedJob, job))
				}
			}
		})
	}
}

func makeTestingPresubmit(name, context string, org, repo, branch string) *prowconfig.Presubmit {
	return &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Agent:  "kubernetes",
			Name:   name,
			Labels: map[string]string{rehearseLabel: "123", "pj-rehearse.openshift.io/can-be-rehearsed": "true"},
			Spec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
				}},
			},
		},
		RerunCommand: "/test pj-rehearse",
		Reporter:     prowconfig.Reporter{Context: context},
		Brancher: prowconfig.Brancher{Branches: []string{
			fmt.Sprintf("^%s$", branch),
		}},
	}
}

func TestMakeRehearsalPresubmit(t *testing.T) {
	testPrNumber := 123
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
	expectedPresubmit := &prowconfig.Presubmit{}
	deepcopy.Copy(expectedPresubmit, sourcePresubmit)

	expectedPresubmit.Name = "rehearse-123-pull-ci-org-repo-branch-test"
	expectedPresubmit.Labels = map[string]string{rehearseLabel: "123"}
	expectedPresubmit.Spec.Containers[0].Args = []string{"arg1", "arg2"}
	expectedPresubmit.RerunCommand = "/test pj-rehearse"
	expectedPresubmit.Context = "ci/rehearse/org/repo/branch/test"
	expectedPresubmit.Optional = true
	expectedPresubmit.ExtraRefs = []pjapi.Refs{
		{
			Org:     "org",
			Repo:    "repo",
			BaseRef: "branch",
			WorkDir: true,
		},
	}

	rehearsal, err := makeRehearsalPresubmit(sourcePresubmit, testRepo, testPrNumber)
	if err != nil {
		t.Errorf("Unexpected error in makeRehearsalPresubmit: %v", err)
	}
	if !equality.Semantic.DeepEqual(expectedPresubmit, rehearsal) {
		t.Errorf("Expected rehearsal Presubmit differs:\n%s", diff.ObjectReflectDiff(expectedPresubmit, rehearsal))
	}
}

func makeTestingProwJob(namespace, jobName, context string, refs *pjapi.Refs, org, repo, branch string) *pjapi.ProwJob {
	return &pjapi.ProwJob{
		TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "generatedTestName",
			Namespace: namespace,
			Labels: map[string]string{
				"created-by-prow":       "true",
				"prow.k8s.io/job":       jobName,
				"prow.k8s.io/refs.org":  refs.Org,
				"prow.k8s.io/refs.repo": refs.Repo,
				"prow.k8s.io/type":      "presubmit",
				"prow.k8s.io/refs.pull": strconv.Itoa(refs.Pulls[0].Number),
				rehearseLabel:           strconv.Itoa(refs.Pulls[0].Number),
			},
			Annotations: map[string]string{"prow.k8s.io/job": jobName},
		},
		Spec: pjapi.ProwJobSpec{
			Agent:        "kubernetes",
			Type:         pjapi.PresubmitJob,
			Job:          jobName,
			Refs:         refs,
			Report:       true,
			Context:      context,
			RerunCommand: "/test pj-rehearse",
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
				}},
			},
		},
		Status: pjapi.ProwJobStatus{
			State: pjapi.TriggeredState,
		},
	}
}

func makeTestData() (int, string, string, *pjapi.Refs) {
	testPrNumber := 123
	testNamespace := "test-namespace"
	testRefs := &pjapi.Refs{
		Org:     "testOrg",
		Repo:    "testRepo",
		BaseRef: "testBaseRef",
		BaseSHA: "testBaseSHA",
		Pulls:   []pjapi.Pull{{Number: testPrNumber, Author: "testAuthor", SHA: "testPrSHA"}},
	}
	testReleasePath := "path/to/openshift/release"

	return testPrNumber, testNamespace, testReleasePath, testRefs
}

func makeSuccessfulFinishReactor(watcher watch.Interface, jobs map[string][]prowconfig.Presubmit) func(clientgoTesting.Action) (bool, watch.Interface, error) {
	return func(clientgoTesting.Action) (bool, watch.Interface, error) {
		watcher.Stop()
		n := 0
		for _, jobs := range jobs {
			n += len(jobs)
		}
		ret := watch.NewFakeWithChanSize(n, true)
		for event := range watcher.ResultChan() {
			pj := event.Object.(*pjapi.ProwJob).DeepCopy()
			pj.Status.State = pjapi.SuccessState
			ret.Modify(pj)
		}
		return true, ret, nil
	}
}

func TestExecuteJobsErrors(t *testing.T) {
	testPrNumber, testNamespace, testRepoPath, testRefs := makeTestData()
	targetOrgRepo := "targetOrg/targetRepo"
	targetOrg := "targetOrg"
	targetRepo := "targetRepo"
	testCiopConfigs := config.CompoundCiopConfig{}

	testCases := []struct {
		description  string
		jobs         map[string][]prowconfig.Presubmit
		failToCreate sets.String
	}{{
		description: "fail to Create a prowjob",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", targetOrg, targetRepo, "master"),
		}},
		failToCreate: sets.NewString("rehearse-123-job1"),
	}, {
		description: "fail to Create one of two prowjobs",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", targetOrg, targetRepo, "master"),
			*makeTestingPresubmit("job2", "ci/prow/job2", targetOrg, targetRepo, "master"),
		}},
		failToCreate: sets.NewString("rehearse-123-job2"),
	}}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			testLoggers := Loggers{logrus.New(), logrus.New()}
			fakecs := fake.NewSimpleClientset()
			fakeclient := fakecs.ProwV1().ProwJobs(testNamespace)
			watcher, err := fakeclient.Watch(metav1.ListOptions{})
			if err != nil {
				t.Fatalf("Failed to setup watch: %v", err)
			}
			fakecs.Fake.PrependWatchReactor("prowjobs", makeSuccessfulFinishReactor(watcher, tc.jobs))
			fakecs.Fake.PrependReactor("create", "prowjobs", func(action clientgoTesting.Action) (bool, runtime.Object, error) {
				createAction := action.(clientgoTesting.CreateAction)
				pj := createAction.GetObject().(*pjapi.ProwJob)
				if tc.failToCreate.Has(pj.Spec.Job) {
					return true, nil, fmt.Errorf("fail")
				}
				return false, nil, nil
			})

			jc := NewJobConfigurer(testCiopConfigs, testPrNumber, testLoggers, true, nil, nil, makeBaseRefs())

			presubmits := jc.ConfigurePresubmitRehearsals(tc.jobs)
			executor := NewExecutor(presubmits, testPrNumber, testRepoPath, testRefs, true, testLoggers, fakeclient)
			_, err = executor.ExecuteJobs()

			if err == nil {
				t.Errorf("Expected to return error, got nil")
			}
		})
	}
}

func TestExecuteJobsUnsuccessful(t *testing.T) {
	testPrNumber, testNamespace, testRepoPath, testRefs := makeTestData()
	targetOrg := "targetOrg"
	targetRepo := "targetRepo"
	targetOrgRepo := "targetOrg/targetRepo"
	testCiopConfigs := config.CompoundCiopConfig{}

	testCases := []struct {
		description string
		jobs        map[string][]prowconfig.Presubmit
		results     map[string]pjapi.ProwJobState
	}{{
		description: "single job that fails",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", targetOrg, targetRepo, "master"),
		}},
		results: map[string]pjapi.ProwJobState{"rehearse-123-job1": pjapi.FailureState},
	}, {
		description: "single job that aborts",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", targetOrg, targetRepo, "master"),
		}},
		results: map[string]pjapi.ProwJobState{"rehearse-123-job1": pjapi.AbortedState},
	}, {
		description: "one job succeeds, one fails",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", targetOrg, targetRepo, "master"),
			*makeTestingPresubmit("job2", "ci/prow/job2", targetOrg, targetRepo, "master"),
		}},
		results: map[string]pjapi.ProwJobState{
			"rehearse-123-job1": pjapi.SuccessState,
			"rehearse-123-job2": pjapi.FailureState,
		},
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			testLoggers := Loggers{logrus.New(), logrus.New()}
			fakecs := fake.NewSimpleClientset()
			fakeclient := fakecs.ProwV1().ProwJobs(testNamespace)
			watcher, err := fakeclient.Watch(metav1.ListOptions{})
			if err != nil {
				t.Fatalf("Failed to setup watch: %v", err)
			}
			fakecs.Fake.PrependWatchReactor("prowjobs", func(clientgoTesting.Action) (bool, watch.Interface, error) {
				watcher.Stop()
				n := 0
				for _, jobs := range tc.jobs {
					n += len(jobs)
				}
				ret := watch.NewFakeWithChanSize(n, true)
				for event := range watcher.ResultChan() {
					pj := event.Object.(*pjapi.ProwJob).DeepCopy()
					pj.Status.State = tc.results[pj.Spec.Job]
					ret.Modify(pj)
				}
				return true, ret, nil
			})

			jc := NewJobConfigurer(testCiopConfigs, testPrNumber, testLoggers, true, nil, nil, makeBaseRefs())
			presubmits := jc.ConfigurePresubmitRehearsals(tc.jobs)
			executor := NewExecutor(presubmits, testPrNumber, testRepoPath, testRefs, false, testLoggers, fakeclient)
			success, _ := executor.ExecuteJobs()

			if success {
				t.Errorf("Expected to return success=false, got true")
			}
		})
	}
}

func TestExecuteJobsPositive(t *testing.T) {
	testPrNumber, testNamespace, testRepoPath, testRefs := makeTestData()
	rehearseJobContextTemplate := "ci/rehearse/%s/%s/%s"
	targetOrgRepo := "targetOrg/targetRepo"
	anotherTargetOrgRepo := "anotherOrg/anotherRepo"
	targetOrg := "targetOrg"
	targetRepo := "targetRepo"
	anotherTargetOrg := "anotherOrg"
	anotherTargetRepo := "anotherRepo"
	testCiopConfigs := config.CompoundCiopConfig{}

	testCases := []struct {
		description  string
		jobs         map[string][]prowconfig.Presubmit
		expectedJobs []pjapi.ProwJobSpec
	}{{
		description: "two jobs in a single repo",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", targetOrg, targetRepo, "master"),
			*makeTestingPresubmit("job2", "ci/prow/job2", targetOrg, targetRepo, "master"),
		}},
		expectedJobs: []pjapi.ProwJobSpec{
			makeTestingProwJob(testNamespace,
				"rehearse-123-job1",
				fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "master", "job1"),
				testRefs, targetOrg, targetRepo, "master").Spec,
			makeTestingProwJob(testNamespace,
				"rehearse-123-job2",
				fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "master", "job2"),
				testRefs, targetOrg, targetRepo, "master").Spec,
		}}, {
		description: "two jobs in a single repo, same context but different branch",
		jobs: map[string][]prowconfig.Presubmit{targetOrgRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", targetOrg, targetRepo, "master"),
			*makeTestingPresubmit("job2", "ci/prow/job2", targetOrg, targetRepo, "not-master"),
		}},
		expectedJobs: []pjapi.ProwJobSpec{
			makeTestingProwJob(testNamespace,
				"rehearse-123-job1",
				fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "master", "job1"),
				testRefs, targetOrg, targetRepo, "master").Spec,
			makeTestingProwJob(testNamespace,
				"rehearse-123-job2",
				fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "not-master", "job2"),
				testRefs, targetOrg, targetRepo, "not-master").Spec,
		}},
		{
			description: "two jobs in a separate repos",
			jobs: map[string][]prowconfig.Presubmit{
				targetOrgRepo:        {*makeTestingPresubmit("job1", "ci/prow/job1", targetOrg, targetRepo, "master")},
				anotherTargetOrgRepo: {*makeTestingPresubmit("job2", "ci/prow/job2", anotherTargetOrg, anotherTargetRepo, "master")},
			},
			expectedJobs: []pjapi.ProwJobSpec{
				makeTestingProwJob(testNamespace,
					"rehearse-123-job1",
					fmt.Sprintf(rehearseJobContextTemplate, targetOrgRepo, "master", "job1"),
					testRefs, targetOrg, targetRepo, "master").Spec,
				makeTestingProwJob(testNamespace,
					"rehearse-123-job2",
					fmt.Sprintf(rehearseJobContextTemplate, anotherTargetOrgRepo, "master", "job2"),
					testRefs, anotherTargetOrg, anotherTargetRepo, "master").Spec,
			},
		}, {
			description:  "no jobs",
			jobs:         map[string][]prowconfig.Presubmit{},
			expectedJobs: []pjapi.ProwJobSpec{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			testLoggers := Loggers{logrus.New(), logrus.New()}
			fakecs := fake.NewSimpleClientset()
			fakeclient := fakecs.ProwV1().ProwJobs(testNamespace)
			watcher, err := fakeclient.Watch(metav1.ListOptions{})
			if err != nil {
				t.Fatalf("Failed to setup watch: %v", err)
			}
			fakecs.Fake.PrependWatchReactor("prowjobs", makeSuccessfulFinishReactor(watcher, tc.jobs))

			jc := NewJobConfigurer(testCiopConfigs, testPrNumber, testLoggers, true, nil, nil, makeBaseRefs())
			presubmits := jc.ConfigurePresubmitRehearsals(tc.jobs)
			executor := NewExecutor(presubmits, testPrNumber, testRepoPath, testRefs, true, testLoggers, fakeclient)
			success, err := executor.ExecuteJobs()

			if err != nil {
				t.Errorf("Expected ExecuteJobs() to not return error, returned %v", err)
				return
			}

			if !success {
				t.Errorf("Expected ExecuteJobs() to return success=true, got false")
			}

			createdJobs, err := fakeclient.List(metav1.ListOptions{})
			if err != nil {
				t.Errorf("Failed to get expected ProwJobs from fake client")
				return
			}

			var createdJobSpecs []pjapi.ProwJobSpec
			for _, job := range createdJobs.Items {
				createdJobSpecs = append(createdJobSpecs, job.Spec)
			}

			// Sort to allow comparison
			sort.Slice(tc.expectedJobs, func(a, b int) bool { return tc.expectedJobs[a].Job < tc.expectedJobs[b].Job })
			sort.Slice(createdJobSpecs, func(a, b int) bool { return createdJobSpecs[a].Job < createdJobSpecs[b].Job })

			if !equality.Semantic.DeepEqual(tc.expectedJobs, createdJobSpecs) {
				t.Errorf("Created ProwJobs differ from expected:\n%s", diff.ObjectReflectDiff(tc.expectedJobs, createdJobSpecs))
			}
		})
	}
}

func TestWaitForJobs(t *testing.T) {
	loggers := Loggers{logrus.New(), logrus.New()}
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
		pjs     sets.String
		events  []*pjapi.ProwJob
		success bool
		err     error
	}{{
		id:      "empty",
		success: true,
	}, {
		id:      "one successful job",
		success: true,
		pjs:     sets.NewString("success0"),
		events:  []*pjapi.ProwJob{&pjSuccess0},
	}, {
		id:  "mixed states",
		pjs: sets.NewString("failure", "success0", "aborted", "error"),
		events: []*pjapi.ProwJob{
			&pjFailure, &pjPending, &pjSuccess0,
			&pjTriggered, &pjAborted, &pjError,
		},
	}, {
		id:      "ignored states",
		success: true,
		pjs:     sets.NewString("success0"),
		events:  []*pjapi.ProwJob{&pjPending, &pjSuccess0, &pjTriggered},
	}, {
		id:      "repeated events",
		success: true,
		pjs:     sets.NewString("success0", "success1"),
		events:  []*pjapi.ProwJob{&pjSuccess0, &pjSuccess0, &pjSuccess1},
	}, {
		id:  "repeated events with failure",
		pjs: sets.NewString("success0", "success1", "failure"),
		events: []*pjapi.ProwJob{
			&pjSuccess0, &pjSuccess0,
			&pjSuccess1, &pjFailure,
		},
	}, {
		id:      "not watched",
		success: true,
		pjs:     sets.NewString("success1"),
		events:  []*pjapi.ProwJob{&pjSuccess0, &pjFailure, &pjSuccess1},
	}, {
		id:     "not watched failure",
		pjs:    sets.NewString("failure"),
		events: []*pjapi.ProwJob{&pjSuccess0, &pjFailure},
	}}
	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			w := watch.NewFakeWithChanSize(len(tc.events), true)
			for _, j := range tc.events {
				w.Modify(j)
			}
			cs := fake.NewSimpleClientset()
			cs.Fake.PrependWatchReactor("prowjobs", func(clientgoTesting.Action) (bool, watch.Interface, error) {
				return true, w, nil
			})

			executor := NewExecutor(nil, 0, "", &pjapi.Refs{}, true, loggers, cs.ProwV1().ProwJobs("test"))
			success, err := executor.waitForJobs(tc.pjs, "")
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
	empty := watch.NewEmptyWatch()
	mod := watch.NewFakeWithChanSize(1, true)
	mod.Modify(&pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "j"},
		Status:     pjapi.ProwJobStatus{State: pjapi.SuccessState},
	})
	ws := []watch.Interface{empty, mod}
	cs := fake.NewSimpleClientset()
	cs.Fake.PrependWatchReactor("prowjobs", func(clientgoTesting.Action) (_ bool, ret watch.Interface, _ error) {
		ret, ws = ws[0], ws[1:]
		return true, ret, nil
	})

	executor := NewExecutor(nil, 0, "", &pjapi.Refs{}, true, Loggers{logrus.New(), logrus.New()}, cs.ProwV1().ProwJobs("test"))
	success, err := executor.waitForJobs(sets.String{"j": {}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !success {
		t.Fail()
	}
}

func TestWaitForJobsLog(t *testing.T) {
	jobLogger, jobHook := logrustest.NewNullLogger()
	dbgLogger, dbgHook := logrustest.NewNullLogger()
	dbgLogger.SetLevel(logrus.DebugLevel)
	w := watch.NewFakeWithChanSize(2, true)
	w.Modify(&pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "success"},
		Status:     pjapi.ProwJobStatus{State: pjapi.SuccessState}})
	w.Modify(&pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "failure"},
		Status:     pjapi.ProwJobStatus{State: pjapi.FailureState}})
	cs := fake.NewSimpleClientset()
	cs.Fake.PrependWatchReactor("prowjobs", func(clientgoTesting.Action) (bool, watch.Interface, error) {
		return true, w, nil
	})
	loggers := Loggers{jobLogger, dbgLogger}

	executor := NewExecutor(nil, 0, "", &pjapi.Refs{}, true, loggers, cs.ProwV1().ProwJobs("test"))
	_, err := executor.waitForJobs(sets.NewString("success", "failure"), "")
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
	check(jobHook, "success", logrus.InfoLevel, &successState)
	check(jobHook, "failure", logrus.ErrorLevel, &failureState)
	check(dbgHook, "success", logrus.DebugLevel, nil)
	check(dbgHook, "failure", logrus.DebugLevel, nil)
}

func TestFilterPresubmits(t *testing.T) {
	labels := map[string]string{"pj-rehearse.openshift.io/can-be-rehearsed": "true"}

	testCases := []struct {
		description    string
		volumesAllowed bool
		crippleFunc    func(*prowconfig.Presubmit) map[string][]prowconfig.Presubmit
		expected       func(*prowconfig.Presubmit) config.Presubmits
	}{
		{
			description: "jobs that need additional volumes mounted, not allowed",
			crippleFunc: func(j *prowconfig.Presubmit) map[string][]prowconfig.Presubmit {
				j.Spec.Volumes = []v1.Volume{{Name: "volume"}}
				return map[string][]prowconfig.Presubmit{"org/repo": {*j}}
			},
			expected: func(j *prowconfig.Presubmit) config.Presubmits {
				return config.Presubmits{}
			},
		},
		{
			description:    "jobs that need additional volumes mounted, allowed",
			volumesAllowed: true,
			crippleFunc: func(j *prowconfig.Presubmit) map[string][]prowconfig.Presubmit {
				j.Spec.Volumes = []v1.Volume{{Name: "volume"}}
				j.Labels = labels
				return map[string][]prowconfig.Presubmit{"org/repo": {*j}}
			},
			expected: func(j *prowconfig.Presubmit) config.Presubmits {
				j.Spec.Volumes = []v1.Volume{{Name: "volume"}}
				return config.Presubmits{"org/repo": {*j}}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			basePresubmit := makeBasePresubmit()
			tc.crippleFunc(basePresubmit)
			p := filterPresubmits(map[string][]prowconfig.Presubmit{"org/repo": {*basePresubmit}}, tc.volumesAllowed, logrus.New())

			expected := tc.expected(basePresubmit)
			if !equality.Semantic.DeepEqual(expected, p) {
				t.Fatalf("Found: %#v\nExpected: %#v", p, expected)
			}
		})

	}
}

func makeBasePresubmit() *prowconfig.Presubmit {
	return &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Agent: "kubernetes",
			Name:  "pull-ci-organization-repo-master-test",
			Labels: map[string]string{"ci.openshift.org/rehearse": "123",
				"pj-rehearse.openshift.io/can-be-rehearsed": "true"},
			Spec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
					Args:    []string{"arg"},
				}},
			},
		},
		RerunCommand: "/test pj-rehearse",
		Reporter:     prowconfig.Reporter{Context: "ci/prow/test"},
		Brancher:     prowconfig.Brancher{Branches: []string{"^master$"}},
	}
}

func TestReplaceCMTemplateName(t *testing.T) {
	templates := map[string]string{
		"test-template.yaml":  "rehearse-template-test-template-00000000",
		"test-template2.yaml": "rehearse-template-test-template-11111111",
		"test-template3.yaml": "rehearse-template-test-template-22222222",
	}

	testCases := []struct {
		description     string
		jobVolumeMounts []v1.VolumeMount
		jobVolumes      []v1.Volume
		expectedToFind  func() []v1.Volume
	}{
		{
			description:     "no volumes",
			jobVolumeMounts: []v1.VolumeMount{},
			jobVolumes:      []v1.Volume{},
			expectedToFind:  func() []v1.Volume { return []v1.Volume{} },
		},
		{
			description: "find one in multiple volumes",
			jobVolumeMounts: []v1.VolumeMount{
				{
					Name:      "non-template",
					MountPath: "/tmp/test",
				},
				{
					Name:      "job-definition",
					MountPath: "/tmp/test",
					SubPath:   "test-template.yaml",
				},
			},
			jobVolumes: createVolumesHelper("job-definition", "test-template.yaml"),
			expectedToFind: func() []v1.Volume {
				volumes := createVolumesHelper("job-definition", "test-template.yaml")
				for _, volume := range volumes {
					if volume.Name == "job-definition" {
						volume.VolumeSource.ConfigMap.Name = "rehearse-template-test-template-00000000"
					}
				}
				return volumes
			},
		},
		{
			description: "find one in multiple volumes that for some reason use two templates",
			jobVolumeMounts: []v1.VolumeMount{
				{
					Name:      "non-template",
					MountPath: "/tmp/test",
				},
				{
					Name:      "job-definition",
					MountPath: "/tmp/test",
					SubPath:   "test-template.yaml",
				},
			},
			jobVolumes: append(createVolumesHelper("job-definition", "test-template.yaml"), createVolumesHelper("job-definition2", "test-template2.yaml")...),
			expectedToFind: func() []v1.Volume {
				volumes := append(createVolumesHelper("job-definition", "test-template.yaml"), createVolumesHelper("job-definition2", "test-template2.yaml")...)
				volumes[2].VolumeSource.ConfigMap.Name = "rehearse-template-test-template-00000000"
				return volumes
			},
		},
		{
			description: "find nothing in multiple volumes that use a template that is not changed",
			jobVolumeMounts: []v1.VolumeMount{
				{
					Name:      "non-template",
					MountPath: "/tmp/test",
				},
				{
					Name:      "job-definition",
					MountPath: "/tmp/test",
					SubPath:   "test-template5.yaml",
				},
			},
			jobVolumes: createVolumesHelper("job-definition", "test-template5.yaml"),
			expectedToFind: func() []v1.Volume {
				return createVolumesHelper("job-definition", "test-template5.yaml")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			replaceCMTemplateName(testCase.jobVolumeMounts, testCase.jobVolumes, templates)
			expected := testCase.expectedToFind()
			if !reflect.DeepEqual(expected, testCase.jobVolumes) {
				t.Fatalf("Diff found %v", diff.ObjectReflectDiff(expected, testCase.jobVolumes))
			}
		})
	}
}

func createVolumesHelper(name, key string) []v1.Volume {
	volumes := []v1.Volume{
		{
			Name: "test-volume",
			VolumeSource: v1.VolumeSource{
				Projected: &v1.ProjectedVolumeSource{
					Sources: []v1.VolumeProjection{
						{
							Secret: &v1.SecretProjection{
								LocalObjectReference: v1.LocalObjectReference{Name: "test-secret"},
							},
						},
					},
				},
			},
		},
		{
			Name: "test-volume2",
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{},
			},
		},
	}
	volumes = append(volumes, v1.Volume{
		Name: name,
		VolumeSource: v1.VolumeSource{
			ConfigMap: &v1.ConfigMapVolumeSource{
				LocalObjectReference: v1.LocalObjectReference{Name: "cluster-e2e-test-template"},
				Items: []v1.KeyToPath{
					{Key: key},
				},
			},
		},
	})

	return volumes
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
				t.Fatal(diff.ObjectDiff(tc.want, ret))
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
				Number: 39612,
				Author: "droslean",
				SHA:    "bc825725cfe0acebb06a7e0b11c8228f5a3b89c0",
			},
		},
	}
}
