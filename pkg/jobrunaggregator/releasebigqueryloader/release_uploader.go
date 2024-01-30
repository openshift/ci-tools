package releasebigqueryloader

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/pkg/errors"

	"k8s.io/klog/v2"

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

	releases      []string
	ciDataSet     *bigquery.Dataset
	architectures []string
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
		allTags := r.fetchReleaseTags(release)

		for _, tags := range allTags {
			for _, tag := range tags.Tags {
				// Skip release tags that are already in BigQuery
				if _, ok := releaseTagSet[tag.Name]; ok {
					fmt.Fprintf(os.Stderr, "%s is already present, skipping...\n", tag.Name)
					continue
				}

				fmt.Fprintf(os.Stderr, "Fetching tag %s from release controller...\n", tag.Name)
				releaseDetails := r.fetchReleaseDetails(tags.Architecture, release, tag)
				releaseTag, repositories, pullRequests := releaseDetailsToBigQuery(tags.Architecture, tag, releaseDetails)
				// We skip releases that aren't fully baked (i.e. all jobs run and changelog calculated)
				if (releaseTag.Phase != "Accepted" && releaseTag.Phase != "Rejected") || repositories == nil {
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
	}

	return nil
}

func (r *allReleaseUploaderOptions) fetchReleaseDetails(architecture, release string, tag ReleaseTag) ReleaseDetails {
	releaseDetails := ReleaseDetails{}
	releaseName := release
	if architecture != "amd64" {
		releaseName += "-" + architecture
	}

	url := fmt.Sprintf("https://%s.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/release/%s", architecture, releaseName, tag.Name)

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

func (r *allReleaseUploaderOptions) fetchReleaseTags(release string) []ReleaseTags {
	allTags := make([]ReleaseTags, 0)
	for _, arch := range r.architectures {
		tags := ReleaseTags{
			Architecture: arch,
		}
		releaseName := release
		if arch != "amd64" {
			releaseName += "-" + arch
		}
		uri := fmt.Sprintf("https://%s.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/tags", arch, releaseName)
		resp, err := r.httpClient.Get(uri)
		if err != nil {
			panic(err)
		}
		if resp.StatusCode != http.StatusOK {
			klog.Errorf("release controller returned non-200 error code for %s: %d %s", uri, resp.StatusCode, resp.Status)
			continue
		}

		if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
			klog.Errorf("couldn't decode json: %w", err)
			resp.Body.Close()
			continue
		}
		resp.Body.Close()
		allTags = append(allTags, tags)
	}
	return allTags
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

func releaseDetailsToBigQuery(architecture string, tag ReleaseTag, details ReleaseDetails) (*jobrunaggregatorapi.ReleaseTagRow, []jobrunaggregatorapi.ReleaseRepositoryRow, []jobrunaggregatorapi.ReleasePullRequestRow) {
	release := jobrunaggregatorapi.ReleaseTagRow{
		Architecture: architecture,
		ReleaseTag:   details.Name,
		Phase:        tag.Phase,
	}
	// 4.10.0-0.nightly-2021-11-04-001635 -> 4.10
	parts := strings.Split(details.Name, ".")
	if len(parts) >= 2 {
		release.Release = strings.Join(parts[:2], ".")
	}

	// Get "nightly" or "ci" from the string
	if len(parts) >= 4 {
		stream := strings.Split(parts[3], "-")
		if len(stream) >= 2 {
			release.Stream = stream[0]
		}
	}

	dateTime := regexp.MustCompile(`.*([0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6})`)
	match := dateTime.FindStringSubmatch(tag.Name)
	if len(match) > 1 {
		t, err := time.Parse("2006-01-02-150405", match[1])
		if err == nil {
			release.ReleaseTime = t
		}
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
				Name:           id,
				ReleaseTag:     details.Name,
				JobName:        platform,
				Kind:           "Blocking",
				State:          jobResult.State,
				URL:            jobResult.URL,
				Retries:        jobResult.Retries,
				TransitionTime: jobResult.TransitionTime,
			}
		}
	}

	if jobs, ok := details.Results["informingJobs"]; ok {
		for platform, jobResult := range jobs {
			id := idFromURL(jobResult.URL)
			results[id] = &jobrunaggregatorapi.ReleaseJobRunRow{
				Name:           id,
				ReleaseTag:     details.Name,
				JobName:        platform,
				Kind:           "Informing",
				State:          jobResult.State,
				URL:            jobResult.URL,
				Retries:        jobResult.Retries,
				TransitionTime: jobResult.TransitionTime,
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
