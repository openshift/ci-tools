package main

import (
	"fmt"
	"path/filepath"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/jobconfig"
)

var (
	unexportedFields = []cmp.Option{
		cmpopts.IgnoreUnexported(prowconfig.Presubmit{}),
		cmpopts.IgnoreUnexported(prowconfig.Periodic{}),
		cmpopts.IgnoreUnexported(prowconfig.Brancher{}),
		cmpopts.IgnoreUnexported(prowconfig.RegexpChangeMatcher{}),
	}
)

func validateJobs(o options, clusters []string) error {
	metadata := RepoMetadata()
	dir := filepath.Join(o.releaseRepo, "ci-operator", "jobs", metadata.Org, metadata.Repo)
	jobConfigs, err := jobconfig.ReadFromDir(dir)
	if err != nil {
		return err
	}

	for _, cluster := range clusters {
		jobName := RepoMetadata().JobName(jobconfig.PresubmitPrefix, cluster+"-dry")
		var foundDryJob bool
		for _, job := range jobConfigs.PresubmitsStatic["openshift/release"] {
			if job.Name == jobName {
				foundDryJob = true
				expected := generatePresubmit(cluster)
				if job.Labels[jobconfig.LabelGenerator] != string(generator) {
					return fmt.Errorf("missing label %s=%s on job %s", jobconfig.LabelGenerator, string(generator), jobName)
				}
				delete(job.Labels, jobconfig.LabelGenerator)
				if diff := cmp.Diff(expected, job, unexportedFields...); diff != "" {
					fmt.Println(diff)
					return fmt.Errorf("unexpected diff on job %s", jobName)
				}
			}
		}
		if !foundDryJob {
			return fmt.Errorf("failed to find job %s in dir %s", jobName, dir)
		}

		jobName = RepoMetadata().JobName(jobconfig.PostsubmitPrefix, cluster+"-apply")
		var foundApplyJob bool
		for _, job := range jobConfigs.PostsubmitsStatic["openshift/release"] {
			if job.Name == jobName {
				foundApplyJob = true
				expected := generatePostsubmit(cluster)
				if job.Labels[jobconfig.LabelGenerator] != string(generator) {
					return fmt.Errorf("missing label %s=%s on job %s", jobconfig.LabelGenerator, string(generator), jobName)
				}
				delete(job.Labels, jobconfig.LabelGenerator)
				if diff := cmp.Diff(expected, job, unexportedFields...); diff != "" {
					fmt.Println(diff)
					return fmt.Errorf("unexpected diff on job %s", jobName)
				}
			}
		}
		if !foundApplyJob {
			return fmt.Errorf("failed to find job %s in dir %s", jobName, dir)
		}

		jobName = RepoMetadata().SimpleJobName(jobconfig.PeriodicPrefix, cluster+"-apply")
		var foundPeriodicJob bool
		for _, job := range jobConfigs.Periodics {
			if job.Name == jobName {
				foundPeriodicJob = true
				expected := generatePeriodic(cluster)
				if job.Labels[jobconfig.LabelGenerator] != string(generator) {
					return fmt.Errorf("missing label %s=%s on job %s", jobconfig.LabelGenerator, string(generator), jobName)
				}
				delete(job.Labels, jobconfig.LabelGenerator)
				if diff := cmp.Diff(expected, job, unexportedFields...); diff != "" {
					fmt.Println(diff)
					return fmt.Errorf("unexpected diff on job %s", jobName)
				}
			}
		}
		if !foundPeriodicJob {
			return fmt.Errorf("failed to find job %s in dir %s", jobName, dir)
		}
	}
	return nil
}
