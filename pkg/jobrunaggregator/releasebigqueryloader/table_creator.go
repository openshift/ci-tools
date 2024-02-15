package releasebigqueryloader

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/bigquery"

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
	if err != nil {
		schema, err := bigquery.InferSchema(jobrunaggregatorapi.ReleaseTagRow{})
		if err != nil {
			return err
		}
		if err := releaseTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "table already exists: %s\n", jobrunaggregatorlib.ReleaseTableName)
	}

	// Create release runs table
	releaseJobRunTable := r.ciDataSet.Table(jobrunaggregatorlib.ReleaseJobRunTableName)
	_, err = releaseJobRunTable.Metadata(ctx)
	if err != nil {
		schema, err := bigquery.InferSchema(jobrunaggregatorapi.ReleaseJobRunRow{})
		if err != nil {
			return err
		}
		if err := releaseJobRunTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "table already exists: %s\n", jobrunaggregatorlib.ReleaseJobRunTableName)
	}

	// Create release repositories
	repositoryTable := r.ciDataSet.Table(jobrunaggregatorlib.ReleaseRepositoryTableName)
	_, err = repositoryTable.Metadata(ctx)
	if err != nil {
		schema, err := bigquery.InferSchema(jobrunaggregatorapi.ReleaseRepositoryRow{})
		if err != nil {
			return err
		}
		if err := repositoryTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "table already exists: %s\n", jobrunaggregatorlib.ReleaseRepositoryTableName)
	}

	// Create release pull requests
	pullRequestTable := r.ciDataSet.Table(jobrunaggregatorlib.ReleasePullRequestsTableName)
	_, err = pullRequestTable.Metadata(ctx)
	if err != nil {
		schema, err := bigquery.InferSchema(jobrunaggregatorapi.ReleasePullRequestRow{})
		if err != nil {
			return err
		}
		if err := pullRequestTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "table already exists: %s\n", jobrunaggregatorlib.ReleasePullRequestsTableName)
	}

	return nil
}
