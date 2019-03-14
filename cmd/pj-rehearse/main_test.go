package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"

	clienttesting "k8s.io/client-go/testing"

	"k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/client/clientset/versioned/fake"
	prowconfig "k8s.io/test-infra/prow/config"
	prowgithub "k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/kube"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
	"github.com/openshift/ci-operator-prowgen/pkg/diffs"
	"github.com/openshift/ci-operator-prowgen/pkg/rehearse"
)

const (
	candidatePath    = "../../test/pj-rehearse-integration/candidate"
	masterPath       = "../../test/pj-rehearse-integration/master"
	expectedJobsFile = "../../test/pj-rehearse-integration/expected_jobs.yaml"
)

func TestIntegration(t *testing.T) {
	jobSpec := pjdwapi.JobSpec{
		Type:      kube.PresubmitJob,
		Job:       "pull-pj-rehearse-demo",
		BuildID:   "0",
		ProwJobID: "prowjob",
		Refs: &kube.Refs{
			Org:     "super",
			Repo:    "duper",
			BaseRef: "base-ref",
			BaseSHA: "base-sha",
			Pulls: []v1.Pull{{
				Number: 1234,
				Author: "dptp-team",
				SHA:    "a4dc6278a189aa66ae9c2a0f11834499f104fcd4",
			}},
		},
	}

	logger := logrus.WithFields(logrus.Fields{prowgithub.OrgLogField: jobSpec.Refs.Org, prowgithub.RepoLogField: jobSpec.Refs.Repo})
	loggers := rehearse.Loggers{Job: logger, Debug: logger}
	fakecs := fake.NewSimpleClientset()
	fakeclient := fakecs.ProwV1().ProwJobs("test-namespace")

	changedPresubmits, err := getRehersalsHelper(logger, jobSpec.Refs.Pulls[0].Number)
	if err != nil {
		t.Fatal(err)
	}

	watcher, err := fakeclient.Watch(metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to setup watch: %v", err)
	}

	toBeRehearsed := getExpectedProwJobs(t)
	rehearsed := sets.NewString()

	fakecs.Fake.PrependWatchReactor("prowjobs", func(clienttesting.Action) (bool, watch.Interface, error) {
		watcher.Stop()
		ret := watch.NewFakeWithChanSize(toBeRehearsed.Len(), true)

		for event := range watcher.ResultChan() {
			pj := event.Object.(*v1.ProwJob).DeepCopy()

			if !toBeRehearsed.Has(pj.Spec.Job) {
				t.Fatalf("Job %s shouldn't be rehearsed", pj.Spec.Job)
			}

			if rehearsed.Has(pj.Spec.Job) {
				t.Fatalf("Job %s has already been rehearsed", pj.Spec.Job)
			}

			pj.Status.State = v1.SuccessState
			ret.Modify(pj)

			rehearsed.Insert(pj.Spec.Job)
		}
		return true, ret, nil
	})

	executor := rehearse.NewExecutor(changedPresubmits, jobSpec.Refs.Pulls[0].Number, candidatePath, jobSpec.Refs, false, loggers, fakeclient)
	if _, err := executor.ExecuteJobs(); err != nil {
		t.Fatalf("Failed to execute rehearsal jobs: %v", err)
	}

	if extra := rehearsed.Difference(toBeRehearsed); extra.Len() > 0 {
		t.Fatalf("found unexpectedly rehearsed prowJobs: %v", extra.List())
	}
	if missing := toBeRehearsed.Difference(rehearsed); missing.Len() > 0 {
		t.Fatalf("did not rehearse expected prowJobs: %v", missing.List())
	}
}

func getExpectedProwJobs(t *testing.T) sets.String {
	var expectedJobs []v1.ProwJob
	expectedJobsNames := sets.NewString()

	content, err := ioutil.ReadFile(expectedJobsFile)
	if err != nil {
		t.Fatal(err)
	}

	err = yaml.Unmarshal(content, &expectedJobs)
	if err != nil {
		t.Fatal(err)
	}

	for _, job := range expectedJobs {
		expectedJobsNames.Insert(job.Spec.Job)
	}

	return expectedJobsNames
}

func getRehersalsHelper(logger *logrus.Entry, prNumber int) ([]*prowconfig.Presubmit, error) {
	candidateConfigPath := filepath.Join(candidatePath, config.ConfigInRepoPath)
	candidateJobConfigPath := filepath.Join(candidatePath, config.JobConfigInRepoPath)
	candidateCiopConfigPath := filepath.Join(candidatePath, config.CiopConfigInRepoPath)
	masterCiopConfigPath := filepath.Join(masterPath, config.CiopConfigInRepoPath)
	masterConfigPath := filepath.Join(masterPath, config.ConfigInRepoPath)
	masterJobConfigPath := filepath.Join(masterPath, config.JobConfigInRepoPath)

	prowConfig, err := prowconfig.Load(masterConfigPath, masterJobConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Prow config: %v", err)
	}
	prowPRConfig, err := prowconfig.Load(candidateConfigPath, candidateJobConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Prow config: %v", err)
	}
	ciopPrConfig, err := config.CompoundLoad(candidateCiopConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load ci-operator config: %v", err)
	}
	ciopMasterConfig, err := config.CompoundLoad(masterCiopConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load ci-operator config: %v", err)
	}

	changedPresubmits := diffs.GetChangedPresubmits(prowConfig, prowPRConfig, logger)
	if len(changedPresubmits) == 0 {
		return nil, fmt.Errorf("Empty changedPresubmits was not expected")
	}

	changedCiopConfigs := diffs.GetChangedCiopConfigs(ciopMasterConfig, ciopPrConfig, logger)
	changedPresubmits.AddAll(diffs.GetPresubmitsForCiopConfigs(prowPRConfig, changedCiopConfigs, logger))

	rehearsals := rehearse.ConfigureRehearsalJobs(changedPresubmits, ciopPrConfig, prNumber, rehearse.Loggers{Job: logger, Debug: logger}, false, nil)

	return rehearsals, nil
}
