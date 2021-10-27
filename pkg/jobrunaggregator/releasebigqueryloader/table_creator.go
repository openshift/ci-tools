package releasebigqueryloader

import (
	"cloud.google.com/go/bigquery"
	"context"
	"fmt"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type allReleaseTableCreatorOptions struct {
	ciDataClient jobrunaggregatorlib.CIDataClient
	ciDataSet    *bigquery.Dataset
}

func (r *allReleaseTableCreatorOptions) Run(ctx context.Context) error {
	// Create release tag table
	releaseTable := r.ciDataSet.Table(jobrunaggregatorlib.ReleaseTableName)
	_, err := releaseTable.Metadata(ctx)
	if err == nil {
		return fmt.Errorf("%s table already exists!", jobrunaggregatorlib.ReleaseTableName)
	}
	schema, err := bigquery.InferSchema(jobrunaggregatorapi.ReleaseRow{})
	if err != nil {
		return err
	}
	if err := releaseTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
		return err
	}

	// Create release runs table
	releaseJobRunTable := r.ciDataSet.Table(jobrunaggregatorlib.ReleaseJobRunTableName)
	_, err = releaseJobRunTable.Metadata(ctx)
	if err == nil {
		return fmt.Errorf("%s table already exists!", jobrunaggregatorlib.ReleaseJobRunTableName)
	}
	schema, err = bigquery.InferSchema(jobrunaggregatorapi.ReleaseJobRunRow{})
	if err != nil {
		return err
	}
	if err := releaseJobRunTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
		return err
	}

	return nil
}
