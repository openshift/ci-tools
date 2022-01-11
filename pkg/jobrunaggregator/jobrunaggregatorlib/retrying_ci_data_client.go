package jobrunaggregatorlib

import (
	"context"
	"strings"
	"time"

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

func (c *retryingCIDataClient) GetBackendDisruptionStatisticsByJob(ctx context.Context, jobName string) ([]jobrunaggregatorapi.BackendDisruptionStatisticsRow, error) {
	var ret []jobrunaggregatorapi.BackendDisruptionStatisticsRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetBackendDisruptionStatisticsByJob(ctx, jobName)
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

func (c *retryingCIDataClient) GetLastJobRunWithTestRunDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	var ret *jobrunaggregatorapi.JobRunRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetLastJobRunWithTestRunDataForJobName(ctx, jobName)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) GetLastJobRunWithDisruptionDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	var ret *jobrunaggregatorapi.JobRunRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetLastJobRunWithDisruptionDataForJobName(ctx, jobName)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) GetLastJobRunWithAlertDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	var ret *jobrunaggregatorapi.JobRunRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetLastJobRunWithAlertDataForJobName(ctx, jobName)
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

func (c *retryingCIDataClient) ListReleaseTags(ctx context.Context) (sets.String, error) {
	var ret sets.String
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.ListReleaseTags(ctx)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) GetJobRunForJobNameBeforeTime(ctx context.Context, jobName string, targetTime time.Time) (*jobrunaggregatorapi.JobRunRow, error) {
	var ret *jobrunaggregatorapi.JobRunRow
	err := retry.OnError(slowBackoff, isReadQuotaError, func() error {
		var innerErr error
		ret, innerErr = c.delegate.GetJobRunForJobNameBeforeTime(ctx, jobName, targetTime)
		return innerErr
	})
	return ret, err
}

func (c *retryingCIDataClient) GetJobRunForJobNameAfterTime(ctx context.Context, jobName string, targetTime time.Time) (*jobrunaggregatorapi.JobRunRow, error) {
	var ret *jobrunaggregatorapi.JobRunRow
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
		return true
	}
	return false
}
