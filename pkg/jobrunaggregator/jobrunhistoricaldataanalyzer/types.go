package jobrunhistoricaldataanalyzer

import (
	_ "embed"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

//go:embed pr_message_template.md
var prTemplate string

type parsedJobData struct {
	PercentTimeDiffP50                 float64       `json:"-"`
	PercentTimeDiffP75                 float64       `json:"-"`
	PercentTimeDiffP95                 float64       `json:"-"`
	PercentTimeDiffP99                 float64       `json:"-"`
	TimeDiffP95                        time.Duration `json:"-"`
	TimeDiffP99                        time.Duration `json:"-"`
	PrevP99                            time.Duration `json:"-"`
	PrevP95                            time.Duration `json:"-"`
	PrevP75                            time.Duration `json:"-"`
	PrevP50                            time.Duration `json:"-"`
	DurationP50                        time.Duration `json:"-"`
	DurationP75                        time.Duration `json:"-"`
	DurationP95                        time.Duration `json:"-"`
	DurationP99                        time.Duration `json:"-"`
	JobResults                         int           `json:"-"`
	jobrunaggregatorapi.HistoricalData `json:",inline"`
}

type compareResults struct {
	increaseCount int
	decreaseCount int
	addedJobs     []string
	jobs          []parsedJobData
	missingJobs   []parsedJobData
}
