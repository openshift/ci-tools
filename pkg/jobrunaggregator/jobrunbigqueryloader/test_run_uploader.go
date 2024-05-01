package jobrunbigqueryloader

import (
	"context"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type testRunPendingUploadLister struct {
	tableName    string
	ciDataClient jobrunaggregatorlib.CIDataClient
}

func newTestRunPendingUploadLister(ciDataClient jobrunaggregatorlib.CIDataClient) pendingUploadLister {
	return &testRunPendingUploadLister{
		tableName:    jobrunaggregatorapi.LegacyJobRunTableName,
		ciDataClient: ciDataClient,
	}
}

func (o *testRunPendingUploadLister) getLastUploadedJobRunEndTime(ctx context.Context) (*time.Time, error) {
	return o.ciDataClient.GetLastJobRunEndTimeFromTable(ctx, o.tableName)
}

func (o *testRunPendingUploadLister) listUploadedJobRunIDsSince(ctx context.Context, since *time.Time) (map[string]bool, error) {
	return o.ciDataClient.ListUploadedJobRunIDsSinceFromTable(ctx, o.tableName, since)
}
