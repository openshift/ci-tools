package jobrunaggregatorlib

import (
	"context"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

type retryingCIDataClient struct {
	delegate CIDataClient
}

var _ CIDataClient = &retryingCIDataClient{}

func NewRetryingCIDataClient(delegate CIDataClient) CIDataClient {
	return &retryingCIDataClient{
		delegate: delegate,
	}
}

func (c *retryingCIDataClient) GetBackendDisruptionRowCountByJob(ctx context.Context, jobName, masterNodesUpdated string) (uint64, error) {
	var ret uint64
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetBackendDisruptionRowCountByJob(ctx, jobName, masterNodesUpdated)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) GetBackendDisruptionStatisticsByJob(ctx context.Context, jobName, masterNodesUpdated string) ([]jobrunaggregatorapi.BackendDisruptionStatisticsRow, error) {
	var ret []jobrunaggregatorapi.BackendDisruptionStatisticsRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetBackendDisruptionStatisticsByJob(ctx, jobName, masterNodesUpdated)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) ListAllJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRow, error) {
	var ret []jobrunaggregatorapi.JobRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListAllJobs(ctx)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) ListProwJobRunsSince(ctx context.Context, since *time.Time) ([]*jobrunaggregatorapi.TestPlatformProwJobRow, error) {
	var ret []*jobrunaggregatorapi.TestPlatformProwJobRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListProwJobRunsSince(ctx, since)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) GetLastJobRunEndTimeFromTable(ctx context.Context, tableName string) (*time.Time, error) {
	var ret *time.Time
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetLastJobRunEndTimeFromTable(ctx, tableName)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) ListUploadedJobRunIDsSinceFromTable(ctx context.Context, table string, since *time.Time) (map[string]bool, error) {
	var ret map[string]bool
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListUploadedJobRunIDsSinceFromTable(ctx, table, since)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) GetLastAggregationForJob(ctx context.Context, frequency, jobName string) (*jobrunaggregatorapi.AggregatedTestRunRow, error) {
	var ret *jobrunaggregatorapi.AggregatedTestRunRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetLastAggregationForJob(ctx, frequency, jobName)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) ListUnifiedTestRunsForJobAfterDay(ctx context.Context, jobName string, startDay time.Time) (*UnifiedTestRunRowIterator, error) {
	var ret *UnifiedTestRunRowIterator
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListUnifiedTestRunsForJobAfterDay(ctx, jobName, startDay)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) ListReleaseTags(ctx context.Context) (sets.Set[string], error) {
	var ret sets.Set[string]
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListReleaseTags(ctx)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) GetJobRunForJobNameBeforeTime(ctx context.Context, jobName string, targetTime time.Time) (string, error) {
	var ret string
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetJobRunForJobNameBeforeTime(ctx, jobName, targetTime)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) GetJobRunForJobNameAfterTime(ctx context.Context, jobName string, targetTime time.Time) (string, error) {
	var ret string
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetJobRunForJobNameAfterTime(ctx, jobName, targetTime)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) ListAggregatedTestRunsForJob(ctx context.Context, frequency, jobName string, startDay time.Time) ([]jobrunaggregatorapi.AggregatedTestRunRow, error) {
	var ret []jobrunaggregatorapi.AggregatedTestRunRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListAggregatedTestRunsForJob(ctx, frequency, jobName, startDay)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) ListDisruptionHistoricalData(ctx context.Context) ([]jobrunaggregatorapi.HistoricalData, error) {
	var ret []jobrunaggregatorapi.HistoricalData
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListDisruptionHistoricalData(ctx)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) ListAlertHistoricalData(ctx context.Context) ([]*jobrunaggregatorapi.AlertHistoricalDataRow, error) {
	var ret []*jobrunaggregatorapi.AlertHistoricalDataRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListAlertHistoricalData(ctx)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) ListAllKnownAlerts(ctx context.Context) ([]*jobrunaggregatorapi.KnownAlertRow, error) {
	var ret []*jobrunaggregatorapi.KnownAlertRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListAllKnownAlerts(ctx)
		return innerErr
	})
	return ret, err
}

var slowBackoff = wait.Backoff{
	Steps:    4,
	Duration: 10 * time.Second,
	Factor:   2.0,
	Jitter:   0.1,
	Cap:      200 * time.Second,
}

func isReadQuotaError(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "exceeded quota for concurrent queries") {
		logrus.WithError(err).Warn("hit a read quota error")
		return true
	}
	return false
}
