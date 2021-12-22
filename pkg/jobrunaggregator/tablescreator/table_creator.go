package tablescreator

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/bigquery"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type allJobsTableCreatorOptions struct {
	ciDataClient jobrunaggregatorlib.CIDataClient
	ciDataSet    *bigquery.Dataset
}

func (r *allJobsTableCreatorOptions) Run(ctx context.Context) error {

	// Create JobsTable
	jobsTable := r.ciDataSet.Table(jobrunaggregatorlib.JobsTableName)
	_, err := jobsTable.Metadata(ctx)
	if err != nil {
		schema, err := bigquery.SchemaFromJSON([]byte(jobrunaggregatorapi.JobSchema))
		if err != nil {
			return err
		}
		if err := jobsTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "table already exists: %s\n", jobrunaggregatorlib.JobsTableName)
	}

	// Create JobRunTable
	jobRunsTable := r.ciDataSet.Table(jobrunaggregatorlib.JobRunTableName)
	_, err = jobRunsTable.Metadata(ctx)
	if err != nil {
		schema, err := bigquery.SchemaFromJSON([]byte(jobrunaggregatorapi.JobRunSchema))
		if err != nil {
			return err
		}
		if err := jobRunsTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "table already exists: %s\n", jobrunaggregatorlib.JobRunTableName)
	}

	return nil
}
