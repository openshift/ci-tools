package jobruntestcaseanalyzer

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type ciDataClientTester struct {
}

func (c *ciDataClientTester) ListAllJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRow, error) {
	return c.createJobs(), nil
}

func (c *ciDataClientTester) GetLastJobRunWithTestRunDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) GetLastJobRunWithDisruptionDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) GetLastJobRunWithAlertDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) getLastJobRunWithTestRunDataForJobName(ctx context.Context, tableName, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) GetBackendDisruptionStatisticsByJob(ctx context.Context, jobName string) ([]jobrunaggregatorapi.BackendDisruptionStatisticsRow, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) GetLastAggregationForJob(ctx context.Context, frequency, jobName string) (*jobrunaggregatorapi.AggregatedTestRunRow, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) tableForFrequency(frequency string) (string, error) {
	return "", errors.New("Invalid api call")
}

func (c *ciDataClientTester) ListUnifiedTestRunsForJobAfterDay(ctx context.Context, jobName string, startDay time.Time) (*jobrunaggregatorlib.UnifiedTestRunRowIterator, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) ListReleaseTags(ctx context.Context) (sets.String, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) GetJobRunForJobNameBeforeTime(ctx context.Context, jobName string, targetTime time.Time) (*jobrunaggregatorapi.JobRunRow, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) GetJobRunForJobNameAfterTime(ctx context.Context, jobName string, targetTime time.Time) (*jobrunaggregatorapi.JobRunRow, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) ListAggregatedTestRunsForJob(ctx context.Context, frequency, jobName string, startDay time.Time) ([]jobrunaggregatorapi.AggregatedTestRunRow, error) {
	return nil, errors.New("Invalid api call")
}

func (c *ciDataClientTester) createJobs() []jobrunaggregatorapi.JobRow {
	jobs := make([]jobrunaggregatorapi.JobRow, 3)
	jobs[0] = jobrunaggregatorapi.JobRow{JobName: "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-upgrade", Platform: "metal", Network: "sdn"}
	jobs[1] = jobrunaggregatorapi.JobRow{JobName: "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4", Platform: "metal", Network: "sdn"}
	jobs[2] = jobrunaggregatorapi.JobRow{JobName: "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6", Platform: "metal", Network: "sdn"}

	return jobs
}
