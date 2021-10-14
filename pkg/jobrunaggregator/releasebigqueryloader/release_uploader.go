package releasebigqueryloader

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/civil"
	"github.com/pkg/errors"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type allReleaseUploaderOptions struct {
	ciDataClient jobrunaggregatorlib.CIDataClient
	httpClient   *http.Client

	releaseInserter *bigquery.Inserter
	releaseTable    *bigquery.Table

	releaseJobRunInserter *bigquery.Inserter
	releaseJobRunTable    *bigquery.Table

	releases  []string
	ciDataSet *bigquery.Dataset
}

func (r *allReleaseUploaderOptions) Run(ctx context.Context) error {
	if err := r.findOrCreateTable(ctx, jobrunaggregatorlib.ReleaseTableName); err != nil {
		return errors.Wrapf(err, "could not find or create %s table", jobrunaggregatorlib.ReleaseTableName)
	}

	if err := r.findOrCreateTable(ctx, jobrunaggregatorlib.ReleaseJobRunTableName); err != nil {
		return errors.Wrapf(err, "could not find or create %s table", jobrunaggregatorlib.ReleaseJobRunTableName)
	}

	releaseTagSet, err := r.ciDataClient.ListReleaseTags(ctx)
	if err != nil {
		return err
	}

	for _, release := range r.releases {
		fmt.Fprintf(os.Stderr, "Fetching release %s from release controller...\n", release)
		tags := r.fetchReleaseTags(release)

		for _, tag := range tags.Tags {
			fmt.Fprintf(os.Stderr, "Fetching tag %s from release controller...\n", tag.Name)
			releaseDetails := r.fetchReleaseDetails(release, tag)
			bigQueryReleaseDetails := releaseDetailsToBigQuery(tag, releaseDetails)

			// We skip releases that aren't fully baked, or already in the big query tables:
			if _, ok := releaseTagSet[bigQueryReleaseDetails.ReleaseTag]; ok {
				continue
			}

			if bigQueryReleaseDetails.Phase == "Ready" || !bigQueryReleaseDetails.ChangeLog.Valid {
				continue
			}

			runs := releaseJobRunsToBigQuery(releaseDetails)
			if err := r.releaseJobRunInserter.Put(ctx, runs); err != nil {
				return errors.Wrapf(err, "could not insert job runs to table")
			}

			if err := r.releaseInserter.Put(ctx, bigQueryReleaseDetails); err != nil {
				return errors.Wrapf(err, "could not insert release details for %s", tag.Name)
			}
		}
	}

	return nil
}

func (r *allReleaseUploaderOptions) fetchReleaseDetails(release string, tag ReleaseTag) ReleaseDetails {
	releaseDetails := ReleaseDetails{}
	url := fmt.Sprintf("https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/release/%s", release, tag.Name)

	resp, err := r.httpClient.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&releaseDetails); err != nil {
		panic(err)
	}
	return releaseDetails
}

func (r *allReleaseUploaderOptions) fetchReleaseTags(release string) ReleaseTags {
	tags := ReleaseTags{}

	resp, err := r.httpClient.Get(fmt.Sprintf("https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/tags", release))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		panic(err)
	}
	return tags
}

func (r *allReleaseUploaderOptions) findOrCreateTable(ctx context.Context, tableName string) error {
	switch tableName {
	case jobrunaggregatorlib.ReleaseTableName:
		r.releaseTable = r.ciDataSet.Table(jobrunaggregatorlib.ReleaseTableName)
		_, err := r.releaseTable.Metadata(ctx)
		if err != nil {
			schema, err := bigquery.InferSchema(jobrunaggregatorapi.ReleaseRow{})
			if err != nil {
				return err
			}
			if err := r.releaseTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
				return err
			}
		}
		r.releaseInserter = r.releaseTable.Inserter()
	case jobrunaggregatorlib.ReleaseJobRunTableName:
		r.releaseJobRunTable = r.ciDataSet.Table(jobrunaggregatorlib.ReleaseJobRunTableName)
		_, err := r.releaseJobRunTable.Metadata(ctx)
		if err != nil {
			schema, err := bigquery.InferSchema(jobrunaggregatorapi.ReleaseJobRunRow{})
			if err != nil {
				return err
			}
			if err := r.releaseJobRunTable.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
				return err
			}
		}
		r.releaseJobRunInserter = r.releaseJobRunTable.Inserter()
	}

	return nil
}

func releaseDetailsToBigQuery(tag ReleaseTag, details ReleaseDetails) jobrunaggregatorapi.ReleaseRow {
	changeLog := bigquery.NullString{}
	if len(details.ChangeLog) > 0 {
		changeLog = bigquery.NullString{StringVal: string(details.ChangeLog), Valid: true}
	}

	release := jobrunaggregatorapi.ReleaseRow{
		ReleaseTag: details.Name,
		Phase:      tag.Phase,
		ChangeLog:  changeLog,
	}

	return release
}

func releaseJobRunsToBigQuery(details ReleaseDetails) []*jobrunaggregatorapi.ReleaseJobRunRow {
	results := make([]*jobrunaggregatorapi.ReleaseJobRunRow, 0)

	if jobs, ok := details.Results["blockingJobs"]; ok {
		for platform, jobResult := range jobs {
			id := idFromURL(jobResult.URL)
			results = append(results, &jobrunaggregatorapi.ReleaseJobRunRow{
				Name:    id,
				JobName: platform,
				Kind:    "Blocking",
				State:   jobResult.State,
				URL:     jobResult.URL,
				Retries: bigquery.NullInt64{Int64: int64(jobResult.Retries), Valid: true},
				TransitionTime: bigquery.NullDateTime{
					DateTime: civil.DateTimeOf(jobResult.TransitionTime),
					Valid:    !jobResult.TransitionTime.IsZero(),
				},
			})
		}
	}

	if jobs, ok := details.Results["informingJobs"]; ok {
		for platform, jobResult := range jobs {
			id := idFromURL(jobResult.URL)
			results = append(results, &jobrunaggregatorapi.ReleaseJobRunRow{
				Name:    id,
				JobName: platform,
				Kind:    "Informing",
				State:   jobResult.State,
				URL:     jobResult.URL,
				TransitionTime: bigquery.NullDateTime{
					DateTime: civil.DateTimeOf(jobResult.TransitionTime),
					Valid:    !jobResult.TransitionTime.IsZero(),
				},
			})
		}
	}

	for _, upgrade := range append(details.UpgradesTo, details.UpgradesFrom...) {
		for _, run := range upgrade.History {
			id := idFromURL(run.URL)
			results = append(results, &jobrunaggregatorapi.ReleaseJobRunRow{
				Name:    id,
				JobName: run.URL,
				Kind:    "Upgrade",
				State:   run.State,
				TransitionTime: bigquery.NullDateTime{
					DateTime: civil.DateTimeOf(run.TransitionTime),
					Valid:    !run.TransitionTime.IsZero(),
				},
				UpgradesFrom: bigquery.NullString{StringVal: upgrade.From, Valid: true},
				UpgradesTo:   bigquery.NullString{StringVal: upgrade.To, Valid: true},
			})
		}
	}

	return results
}

func idFromURL(prowURL string) string {
	parsed, err := url.Parse(prowURL)
	if err != nil {
		return ""
	}

	return path.Base(parsed.Path)
}
