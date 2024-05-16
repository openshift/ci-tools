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

	tableNamesToSchemas := map[string]string{
		jobrunaggregatorlib.JobsTableName:    jobrunaggregatorapi.JobSchema,
		jobrunaggregatorlib.TestRunTableName: jobrunaggregatorapi.TestRunsSchema,
		jobrunaggregatorlib.JobRunTableName:  jobrunaggregatorapi.JobRunSchema,
	}

	for tableName, tableSchema := range tableNamesToSchemas {

		// Create the table
		bqTable := r.ciDataSet.Table(tableName)
		_, err := bqTable.Metadata(ctx)
		if err != nil {
			schema, err := bigquery.SchemaFromJSON([]byte(tableSchema))
			if err != nil {
				return err
			}
			if err := bqTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stdout, "table already exists: %s\n", tableName)
		}
	}
	return nil
}
