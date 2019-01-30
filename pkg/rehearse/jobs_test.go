package rehearse

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"k8s.io/api/core/v1"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/client/clientset/versioned/fake"
	prowconfig "k8s.io/test-infra/prow/config"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	clientgo_testing "k8s.io/client-go/testing"
)

func makeTestingPresubmitForEnv(env []v1.EnvVar) *prowconfig.Presubmit {
	return &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Name: "test-job-name",
			Spec: &v1.PodSpec{
				Containers: []v1.Container{
					{Env: env},
				},
			},
		},
	}
}

type fakeCiopConfig struct {
	fakeFiles map[string]string
}

func (c *fakeCiopConfig) Load(repo, configFile string) (string, error) {
	fullPath := filepath.Join(repo, configFile)
	content, ok := c.fakeFiles[fullPath]
	if ok {
		return content, nil
	}

	return "", fmt.Errorf("no such fake file")
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
	testTargetRepo := "org/repo"
	testLogger := logrus.New()

	testCases := []struct {
		description   string
		sourceEnv     []v1.EnvVar
		configs       *fakeCiopConfig
		expectedEnv   []v1.EnvVar
		expectedError bool
	}{{
		description: "empty env -> no changes",
		configs:     &fakeCiopConfig{},
	}, {
		description: "no Env.ValueFrom -> no changes",
		sourceEnv:   []v1.EnvVar{{Name: "T", Value: "V"}},
		configs:     &fakeCiopConfig{},
		expectedEnv: []v1.EnvVar{{Name: "T", Value: "V"}},
	}, {
		description: "no Env.ValueFrom.ConfigMapKeyRef -> no changes",
		sourceEnv:   []v1.EnvVar{{Name: "T", ValueFrom: &v1.EnvVarSource{ResourceFieldRef: &v1.ResourceFieldSelector{}}}},
		configs:     &fakeCiopConfig{},
		expectedEnv: []v1.EnvVar{{Name: "T", ValueFrom: &v1.EnvVarSource{ResourceFieldRef: &v1.ResourceFieldSelector{}}}},
	}, {
		description: "CM reference but not ci-operator-configs -> no changes",
		sourceEnv:   []v1.EnvVar{{Name: "T", ValueFrom: makeCMReference("test-cm", "key")}},
		configs:     &fakeCiopConfig{},
		expectedEnv: []v1.EnvVar{{Name: "T", ValueFrom: makeCMReference("test-cm", "key")}},
	}, {
		description: "CM reference to ci-operator-configs -> cm content inlined",
		sourceEnv:   []v1.EnvVar{{Name: "T", ValueFrom: makeCMReference(ciOperatorConfigsCMName, "filename")}},
		configs:     &fakeCiopConfig{fakeFiles: map[string]string{"org/repo/filename": "ciopConfigContent"}},
		expectedEnv: []v1.EnvVar{{Name: "T", Value: "ciopConfigContent"}},
	}, {
		description:   "bad CM key is handled",
		sourceEnv:     []v1.EnvVar{{Name: "T", ValueFrom: makeCMReference(ciOperatorConfigsCMName, "filename")}},
		configs:       &fakeCiopConfig{fakeFiles: map[string]string{}},
		expectedError: true,
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			job := makeTestingPresubmitForEnv(tc.sourceEnv)
			expectedJob := makeTestingPresubmitForEnv(tc.expectedEnv)

			newJob, err := inlineCiOpConfig(job, testTargetRepo, tc.configs, testLogger)

			if tc.expectedError && err == nil {
				t.Errorf("Expected inlineCiopConfig() to return an error, none returned")
				return
			}

			if !tc.expectedError {
				if err != nil {
					t.Errorf("Unexpected error returned by inlineCiOpConfig(): %v", err)
					return
				}

				if !equality.Semantic.DeepEqual(expectedJob, newJob) {
					t.Errorf("Returned job differs from expected:\n%s", diff.ObjectDiff(expectedJob, newJob))
				}
			}
		})
	}
}

func makeTestingPresubmit(name, context string, ciopArgs []string) *prowconfig.Presubmit {
	return &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Name:   name,
			Labels: map[string]string{rehearseLabel: "123"},
			Spec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
					Args:    ciopArgs,
				}},
			},
		},
		Context:  context,
		Brancher: prowconfig.Brancher{Branches: []string{"^master$"}},
	}
}

func TestMakeRehearsalPresubmit(t *testing.T) {
	testCases := []struct {
		source   *prowconfig.Presubmit
		pr       int
		expected *prowconfig.Presubmit
	}{{
		source: makeTestingPresubmit("pull-ci-openshift-ci-operator-master-build", "ci/prow/build", []string{"arg", "arg"}),
		pr:     123,
		expected: makeTestingPresubmit(
			"rehearse-123-pull-ci-openshift-ci-operator-master-build",
			"ci/rehearse/openshift/ci-operator/build",
			[]string{"arg", "arg", "--git-ref=openshift/ci-operator@master"}),
	},
	}
	for _, tc := range testCases {
		rehearsal, err := makeRehearsalPresubmit(tc.source, "openshift/ci-operator", tc.pr)
		if err != nil {
			t.Errorf("Unexpected error in makeRehearsalPresubmit: %v", err)
		}
		if !equality.Semantic.DeepEqual(tc.expected, rehearsal) {
			t.Errorf("Expected rehearsal Presubmit differs:\n%s", diff.ObjectDiff(tc.expected, rehearsal))
		}
	}
}

func TestMakeRehearsalPresubmitNegative(t *testing.T) {
	testName := "pull-ci-organization-repo-master-test"
	testContext := "ci/prow/test"
	testArgs := []string{"arg"}
	testRepo := "organization/repo"
	testPrNumber := 321

	testCases := []struct {
		description string
		crippleFunc func(*prowconfig.Presubmit)
	}{{
		description: "job with multiple containers",
		crippleFunc: func(j *prowconfig.Presubmit) {
			j.Spec.Containers = append(j.Spec.Containers, v1.Container{})
		},
	}, {
		description: "job where command is not `ci-operator`",
		crippleFunc: func(j *prowconfig.Presubmit) {
			j.Spec.Containers[0].Command[0] = "not-ci-operator"
		},
	}, {
		description: "ci-operator job already using --git-ref",
		crippleFunc: func(j *prowconfig.Presubmit) {
			j.Spec.Containers[0].Args = append(j.Spec.Containers[0].Args, "--git-ref=organization/repo@master")
		},
	}, {
		description: "jobs running over multiple branches",
		crippleFunc: func(j *prowconfig.Presubmit) {
			j.Brancher.Branches = append(j.Brancher.Branches, "^feature-branch$")
		},
	}, {
		description: "jobs that need additional volumes mounted",
		crippleFunc: func(j *prowconfig.Presubmit) {
			j.Spec.Volumes = []v1.Volume{{Name: "volume"}}
		},
	},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			job := makeTestingPresubmit(testName, testContext, testArgs)
			tc.crippleFunc(job)
			_, err := makeRehearsalPresubmit(job, testRepo, testPrNumber)
			if err == nil {
				t.Errorf("Expected makeRehearsalPresubmit to return error")
			}
		})
	}
}

func makeTestingProwJob(name, namespace, jobName, context string, refs *pjapi.Refs, ciopArgs []string) *pjapi.ProwJob {
	return &pjapi.ProwJob{
		TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
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
			Type:    pjapi.PresubmitJob,
			Job:     jobName,
			Refs:    refs,
			Report:  true,
			Context: context,
			PodSpec: &v1.PodSpec{
				Containers: []v1.Container{{
					Command: []string{"ci-operator"},
					Args:    ciopArgs,
				}},
			},
		},
		Status: pjapi.ProwJobStatus{
			State: pjapi.TriggeredState,
		},
	}
}

func TestExecuteJobs(t *testing.T) {
	testLogger := logrus.New()
	testPrNumber := 123
	testNamespace := "test-namespace"
	testRepo := "testRepo"
	testOrg := "testOrg"
	testRefs := &pjapi.Refs{
		Org:     testOrg,
		Repo:    testRepo,
		BaseRef: "testBaseRef",
		BaseSHA: "testBaseSHA",
		Pulls:   []pjapi.Pull{{Number: testPrNumber, Author: "testAuthor", SHA: "testPrSHA"}},
	}
	generatedName := "generatedName"
	rehearseJobContextTemplate := "ci/rehearse/%s/%s"

	targetRepo := "targetOrg/targetRepo"
	anotherTargetRepo := "anotherOrg/anotherRepo"

	testCases := []struct {
		description   string
		jobs          map[string][]prowconfig.Presubmit
		expectedError bool
		expectedJobs  []pjapi.ProwJob
	}{{
		description: "two jobs in a single repo",
		jobs: map[string][]prowconfig.Presubmit{targetRepo: {
			*makeTestingPresubmit("job1", "ci/prow/job1", []string{"arg1"}),
			*makeTestingPresubmit("job2", "ci/prow/job2", []string{"arg1"}),
		}},
		expectedJobs: []pjapi.ProwJob{
			*makeTestingProwJob(generatedName,
				testNamespace,
				"rehearse-123-job1",
				fmt.Sprintf(rehearseJobContextTemplate, targetRepo, "job1"),
				testRefs,
				[]string{"arg1", fmt.Sprintf("--git-ref=%s@master", targetRepo)},
			),
			*makeTestingProwJob(generatedName,
				testNamespace,
				"rehearse-123-job2",
				fmt.Sprintf(rehearseJobContextTemplate, targetRepo, "job2"),
				testRefs,
				[]string{"arg1", fmt.Sprintf("--git-ref=%s@master", targetRepo)},
			),
		}},
		{
			description: "two jobs in a separate repos",
			jobs: map[string][]prowconfig.Presubmit{
				targetRepo:        {*makeTestingPresubmit("job1", "ci/prow/job1", []string{"arg1"})},
				anotherTargetRepo: {*makeTestingPresubmit("job2", "ci/prow/job2", []string{"arg1"})},
			},
			expectedJobs: []pjapi.ProwJob{
				*makeTestingProwJob(generatedName,
					testNamespace,
					"rehearse-123-job1",
					fmt.Sprintf(rehearseJobContextTemplate, targetRepo, "job1"),
					testRefs,
					[]string{"arg1", fmt.Sprintf("--git-ref=%s@master", targetRepo)},
				),
				*makeTestingProwJob(generatedName,
					testNamespace,
					"rehearse-123-job2",
					fmt.Sprintf(rehearseJobContextTemplate, anotherTargetRepo, "job2"),
					testRefs,
					[]string{"arg1", fmt.Sprintf("--git-ref=%s@master", anotherTargetRepo)},
				),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakecs := fake.NewSimpleClientset()
			fakeclient := fakecs.ProwV1().ProwJobs(testNamespace)
			watcher, err := fakeclient.Watch(metav1.ListOptions{})
			if err != nil {
				t.Fatalf("Failed to setup watch: %v", err)
			}
			fakecs.Fake.PrependWatchReactor("prowjobs", func(clientgo_testing.Action) (bool, watch.Interface, error) {
				watcher.Stop()
				n := 0
				for _, jobs := range tc.jobs {
					n += len(jobs)
				}
				ret := watch.NewFakeWithChanSize(n, true)
				for event := range watcher.ResultChan() {
					pj := event.Object.(*pjapi.ProwJob).DeepCopy()
					pj.Status.State = pjapi.SuccessState
					ret.Modify(pj)
				}
				return true, ret, nil
			})
			err = ExecuteJobs(tc.jobs, testPrNumber, testRepo, testRefs, true, testLogger, fakeclient)

			if tc.expectedError && err == nil {
				t.Errorf("Expected ExecuteJobs() to return error")
				return
			}

			if !tc.expectedError {
				if err != nil {
					t.Errorf("Expected ExecuteJobs() to not return error, returned %v", err)
					return
				}

				createdJobs, err := fakeclient.List(metav1.ListOptions{})
				if err != nil {
					t.Errorf("Failed to get expected ProwJobs from fake client")
					return
				}

				// Overwrite dynamic struct members to allow comparison
				for i := range createdJobs.Items {
					createdJobs.Items[i].Name = generatedName
					createdJobs.Items[i].Status.StartTime.Reset()
				}

				// Sort to allow comparison
				sort.Slice(tc.expectedJobs, func(a, b int) bool { return tc.expectedJobs[a].Spec.Job < tc.expectedJobs[b].Spec.Job })
				sort.Slice(createdJobs.Items, func(a, b int) bool { return createdJobs.Items[a].Spec.Job < createdJobs.Items[b].Spec.Job })

				if !equality.Semantic.DeepEqual(tc.expectedJobs, createdJobs.Items) {
					t.Errorf("Created ProwJobs differ from expected:\n%s", diff.ObjectDiff(tc.expectedJobs, createdJobs.Items))
				}
			}
		})
	}
}

func TestWaitForJobs(t *testing.T) {
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
			cs.Fake.PrependWatchReactor("prowjobs", func(clientgo_testing.Action) (bool, watch.Interface, error) {
				return true, w, nil
			})
			success, err := waitForJobs(tc.pjs, "", cs.ProwV1().ProwJobs("test"), logrus.New())
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
	cs.Fake.PrependWatchReactor("prowjobs", func(clientgo_testing.Action) (_ bool, ret watch.Interface, _ error) {
		ret, ws = ws[0], ws[1:]
		return true, ret, nil
	})
	success, err := waitForJobs(sets.String{"j": {}}, "", cs.ProwV1().ProwJobs("test"), logrus.New())
	if err != nil {
		t.Fatal(err)
	}
	if !success {
		t.Fail()
	}
}

func TestWaitForJobsLog(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	logger.SetLevel(logrus.DebugLevel)
	w := watch.NewFakeWithChanSize(2, true)
	w.Modify(&pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "success"},
		Status:     pjapi.ProwJobStatus{State: pjapi.SuccessState}})
	w.Modify(&pjapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "failure"},
		Status:     pjapi.ProwJobStatus{State: pjapi.FailureState}})
	cs := fake.NewSimpleClientset()
	cs.Fake.PrependWatchReactor("prowjobs", func(clientgo_testing.Action) (bool, watch.Interface, error) {
		return true, w, nil
	})
	_, err := waitForJobs(sets.NewString("success", "failure"), "", cs.ProwV1().ProwJobs("test"), logger)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, level logrus.Level, state *pjapi.ProwJobState) {
		for _, entry := range hook.Entries {
			if entry.Level == level && entry.Data["name"] == name && (state == nil || entry.Data["state"].(pjapi.ProwJobState) == *state) {
				return
			}
		}
		t.Errorf("no log entry with name == %q, level == %q, and state == %q found", name, level, *state)
	}
	successState, failureState := pjapi.SuccessState, pjapi.FailureState
	check("success", logrus.DebugLevel, nil)
	check("success", logrus.InfoLevel, &successState)
	check("failure", logrus.DebugLevel, nil)
	check("failure", logrus.ErrorLevel, &failureState)
}
