package jobtableprimer

import (
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

type jobRowBuilder struct {
	job *jobrunaggregatorapi.JobRow
}

func newJob(name string) *jobRowBuilder {
	return &jobRowBuilder{
		job: &jobrunaggregatorapi.JobRow{
			JobName:                     name,
			GCSJobHistoryLocationPrefix: "logs/" + name,
			CollectDisruption:           true, // by default we collect disruption
			CollectTestRuns:             true, // by default we collect disruption
		},
	}
}

func (b *jobRowBuilder) WithoutDisruption() *jobRowBuilder {
	b.job.CollectDisruption = false
	return b
}

func (b *jobRowBuilder) WithoutTestRuns() *jobRowBuilder {
	b.job.CollectTestRuns = true
	return b
}

func (b *jobRowBuilder) ToJob() jobrunaggregatorapi.JobRow {
	return *b.job
}
