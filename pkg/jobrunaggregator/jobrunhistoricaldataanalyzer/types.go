package jobrunhistoricaldataanalyzer

import (
	"time"

	_ "embed"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

//go:embed pr_message_template.md
var prTemplate string

type parsedJobData struct {
	NoPrevData                            bool          `json:"-"`
	TimeDiff                              time.Duration `json:"-"`
	DurationP95                           time.Duration `json:"-"`
	DurationP99                           time.Duration `json:"-"`
	jobrunaggregatorapi.HistoricalDataRow `json:",inline"`
}

type compareResults struct {
	increaseCount int
	decreaseCount int
	addedJobs     []string
	jobs          []parsedJobData
	missingJobs   []parsedJobData
}
