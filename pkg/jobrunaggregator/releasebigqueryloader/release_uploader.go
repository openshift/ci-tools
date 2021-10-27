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

	repositoryTableInserter *bigquery.Inserter
	repositoryTable         *bigquery.Table

	pullRequestInserter *bigquery.Inserter
	pullRequestTable    *bigquery.Table

	releases  []string
	ciDataSet *bigquery.Dataset
}

func (r *allReleaseUploaderOptions) Run(ctx context.Context) error {
	if err := r.findTable(ctx, jobrunaggregatorlib.ReleaseTableName); err != nil {
		return errors.Wrapf(err, "could not find %s table", jobrunaggregatorlib.ReleaseTableName)
	}

	if err := r.findTable(ctx, jobrunaggregatorlib.ReleaseJobRunTableName); err != nil {
		return errors.Wrapf(err, "could not find %s table", jobrunaggregatorlib.ReleaseJobRunTableName)
	}

	if err := r.findTable(ctx, jobrunaggregatorlib.ReleaseRepositoryTableName); err != nil {
		return errors.Wrapf(err, "could not find %s table", jobrunaggregatorlib.ReleaseRepositoryTableName)
	}

	if err := r.findTable(ctx, jobrunaggregatorlib.ReleasePullRequestsTableName); err != nil {
		return errors.Wrapf(err, "could not find %s table", jobrunaggregatorlib.ReleasePullRequestsTableName)
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
			releaseTag, repositories, pullRequests := releaseDetailsToBigQuery(tag, releaseDetails)
			// We skip releases that aren't fully baked, or already in the big query tables:
			if releaseTag.Phase == "Ready" || repositories == nil {
				continue
			}
			if _, ok := releaseTagSet[releaseTag.ReleaseTag]; ok {
				continue
			}

			runs := releaseJobRunsToBigQuery(releaseDetails)
			if err := r.releaseJobRunInserter.Put(ctx, runs); err != nil {
				return errors.Wrapf(err, "could not insert job runs to table")
			}

			if len(repositories) != 0 {
				if err := r.repositoryTableInserter.Put(ctx, repositories); err != nil {
					return errors.Wrapf(err, "could not insert repositories to table")
				}
			}

			if len(pullRequests) != 0 {
				if err := r.pullRequestInserter.Put(ctx, pullRequests); err != nil {
					return errors.Wrapf(err, "could not insert pull requests to table")
				}
			}

			if err := r.releaseInserter.Put(ctx, releaseTag); err != nil {
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

func (r *allReleaseUploaderOptions) findTable(ctx context.Context, tableName string) error {
	switch tableName {
	case jobrunaggregatorlib.ReleaseTableName:
		r.releaseTable = r.ciDataSet.Table(jobrunaggregatorlib.ReleaseTableName)
		_, err := r.releaseTable.Metadata(ctx)
		if err != nil {
			return err
		}
		r.releaseInserter = r.releaseTable.Inserter()
	case jobrunaggregatorlib.ReleaseJobRunTableName:
		r.releaseJobRunTable = r.ciDataSet.Table(jobrunaggregatorlib.ReleaseJobRunTableName)
		_, err := r.releaseJobRunTable.Metadata(ctx)
		if err != nil {
			return err
		}
		r.releaseJobRunInserter = r.releaseJobRunTable.Inserter()
	case jobrunaggregatorlib.ReleasePullRequestsTableName:
		r.pullRequestTable = r.ciDataSet.Table(jobrunaggregatorlib.ReleasePullRequestsTableName)
		_, err := r.pullRequestTable.Metadata(ctx)
		if err != nil {
			return err
		}
		r.pullRequestInserter = r.pullRequestTable.Inserter()
	case jobrunaggregatorlib.ReleaseRepositoryTableName:
		r.repositoryTable = r.ciDataSet.Table(jobrunaggregatorlib.ReleaseRepositoryTableName)
		_, err := r.repositoryTable.Metadata(ctx)
		if err != nil {
			return err
		}
		r.repositoryTableInserter = r.repositoryTable.Inserter()

	}

	return nil
}

func releaseDetailsToBigQuery(tag ReleaseTag, details ReleaseDetails) (*jobrunaggregatorapi.ReleaseRow, []jobrunaggregatorapi.ReleaseRepositoryRow, []jobrunaggregatorapi.ReleasePullRequestRow) {
	release := jobrunaggregatorapi.ReleaseRow{
		ReleaseTag: details.Name,
		Phase:      tag.Phase,
	}
	if len(details.ChangeLog) == 0 {
		return &release, nil, nil
	}

	changelog := NewChangelog(tag.Name, string(details.ChangeLog))
	release.KubernetesVersion = changelog.KubernetesVersion()
	release.CurrentOSURL, release.CurrentOSVersion, release.PreviousOSURL, release.PreviousOSVersion, release.OSDiffURL = changelog.CoreOSVersion()
	release.PreviousReleaseTag = changelog.PreviousReleaseTag()
	return &release, changelog.Repositories(), changelog.PullRequests()
}

func releaseJobRunsToBigQuery(details ReleaseDetails) []*jobrunaggregatorapi.ReleaseJobRunRow {
	rows := make([]*jobrunaggregatorapi.ReleaseJobRunRow, 0)
	results := make(map[string]*jobrunaggregatorapi.ReleaseJobRunRow)

	if jobs, ok := details.Results["blockingJobs"]; ok {
		for platform, jobResult := range jobs {
			id := idFromURL(jobResult.URL)
			results[id] = &jobrunaggregatorapi.ReleaseJobRunRow{
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
			}
		}
	}

	if jobs, ok := details.Results["informingJobs"]; ok {
		for platform, jobResult := range jobs {
			id := idFromURL(jobResult.URL)
			results[id] = &jobrunaggregatorapi.ReleaseJobRunRow{
				Name:    id,
				JobName: platform,
				Kind:    "Informing",
				State:   jobResult.State,
				URL:     jobResult.URL,
				TransitionTime: bigquery.NullDateTime{
					DateTime: civil.DateTimeOf(jobResult.TransitionTime),
					Valid:    !jobResult.TransitionTime.IsZero(),
				},
			}
		}
	}

	for _, upgrade := range append(details.UpgradesTo, details.UpgradesFrom...) {
		for _, run := range upgrade.History {
			id := idFromURL(run.URL)
			if result, ok := results[id]; ok {
				result.Upgrade = true
				result.UpgradesFrom = upgrade.From
				result.UpgradesTo = upgrade.To
			}
		}
	}

	for _, result := range results {
		rows = append(rows, result)
	}

	return rows
}

func idFromURL(prowURL string) string {
	parsed, err := url.Parse(prowURL)
	if err != nil {
		return ""
	}

	return path.Base(parsed.Path)
}
