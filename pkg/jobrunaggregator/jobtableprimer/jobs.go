package jobtableprimer

import (
	_ "embed"
	"strings"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

// jobRowListBuilder builds the list of job rows used to prime the job table
type jobRowListBuilder struct {
	releases []jobrunaggregatorapi.ReleaseRow
}

func newJobRowListBuilder(releases []jobrunaggregatorapi.ReleaseRow) *jobRowListBuilder {
	return &jobRowListBuilder{
		releases: releases,
	}
}

func (j *jobRowListBuilder) CreateAllJobRows(jobNames []string) []jobrunaggregatorapi.JobRow {
	jobsRowToCreate := []jobrunaggregatorapi.JobRow{}

	for _, jobName := range jobNames {
		// skip comments
		if strings.HasPrefix(jobName, "//") {
			continue
		}

		// skip empty lines
		jobName = strings.TrimSpace(jobName)
		if len(jobName) == 0 {
			continue
		}

		// skip duplicates.  This happens when periodics redefine release config ones.
		found := false
		for _, existing := range jobsRowToCreate {
			if existing.JobName == jobName {
				found = true
				break
			}
		}
		if found {
			continue
		}

		jobsRowToCreate = append(jobsRowToCreate, newJob(jobName).ToJob())
	}
	return jobsRowToCreate
}
