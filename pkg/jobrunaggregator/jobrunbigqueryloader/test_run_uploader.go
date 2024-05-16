package jobrunbigqueryloader

import (
	"context"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

// pendingJobRunsUploadLister is used to find out which job runs are new for an uploader.
// JobRun data is denormalized with every row in Alerts / BackendDisruption, and thus we query recent
// uploaded job runs by examining these columns.
type pendingJobRunsUploadLister struct {
	tableName    string
	ciDataClient jobrunaggregatorlib.CIDataClient
}

func (o *pendingJobRunsUploadLister) getLastUploadedJobRunEndTime(ctx context.Context) (*time.Time, error) {
	return o.ciDataClient.GetLastJobRunEndTimeFromTable(ctx, o.tableName)
}

func (o *pendingJobRunsUploadLister) listUploadedJobRunIDsSince(ctx context.Context, since *time.Time) (map[string]bool, error) {
	return o.ciDataClient.ListUploadedJobRunIDsSinceFromTable(ctx, o.tableName, since)
}
