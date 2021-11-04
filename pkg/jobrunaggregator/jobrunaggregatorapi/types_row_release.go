package jobrunaggregatorapi

import "cloud.google.com/go/bigquery"

type ReleaseRow struct {
	// Phase contains the overall status of a payload: e.g. Ready, Accepted,
	// Rejected. We do not store Ready payloads in bigquery, as we only want
	// the release after it's "fully baked."
	Phase string `bigquery:"phase"`

	// Release contains the X.Y version of the payload, e.g. 4.8
	Release string `bigquery:"release"`

	// Architecture contains the architecture for the release, e.g. amd64
	Architecture string `bigquery:"architecture"`

	// ReleaseTag contains the release version, e.g. 4.8.0-0.nightly-2021-10-28-013428.
	ReleaseTag string `bigquery:"releaseTag"`

	// PreviousReleaseTag contains the previously accepted build, on which any
	// changelog is based from.
	PreviousReleaseTag string `bigquery:"previousReleaseTag"`

	KubernetesVersion string `bigquery:"kubernetesVersion"`

	// CurrentOSVersion contains the current machine OS version.
	CurrentOSVersion string `bigquery:"currentOSVersion"`

	// PreviousOSVersion, if any, indicates this release included a machine OS
	// upgrade and this field contains the prior version.
	PreviousOSVersion string `bigquery:"previousOSVersion"`

	// CurrentOSURL is a link to the release page for this machine OS version.
	CurrentOSURL string `bigquery:"currentOSURL"`

	// PreviousOSURL is a link to the release page for the previous machine OS version.
	PreviousOSURL string `bigquery:"previousOSURL"`

	// OSDiffURL is a link to the release page diffing the two OS versions.
	OSDiffURL string `bigquery:"osDiffURL"`
}

// ReleaseRepositoryRow represents a repository whose contents was updated in the referenced
// ReleaseTag.
type ReleaseRepositoryRow struct {
	// Name contains the repositories names as they are known in the release payload. It
	// is often but not always the repository name.
	Name string `bigquery:"name"`

	// ReleaseTag is the OpenShift version, e.g. 4.8.0-0.nightly-2021-10-28-013428.
	ReleaseTag string `bigquery:"releaseTag"`

	// Head contains a link to the repository head of this repo.
	Head string `bigquery:"repositoryHead"`

	// FullChangelog contains a link that diffs the contents of this repo
	// from the prior accepted release.
	FullChangelog string `bigquery:"fullChangeLog"`
}

// ReleasePullRequestRow represents a pull request that was included for the first time
// in a release payload.
type ReleasePullRequestRow struct {
	// PullRequestID contains the GitHub pull request number.
	PullRequestID string `bigquery:"pullRequestID"`

	// ReleaseTag is the OpenShift version, e.g. 4.8.0-0.nightly-2021-10-28-013428.
	ReleaseTag string `bigquery:"releaseTag"`

	// Name contains the names as the repository is known in the release payload.
	Name string `bigquery:"name"`

	// Description is the PR description.
	Description string `bigquery:"description"`

	// URL is a link to the pull request.
	URL string `bigquery:"url"`

	// BugURL links to the bug, if any.
	BugURL string `bigquery:"bugURL"`
}

type ReleaseUpgradeRow struct {
	From    string
	To      string
	Success int
	Failure int
	Total   int
}

type ReleaseJobRunRow struct {
	Name           string                `bigquery:"name"`
	JobName        string                `bigquery:"jobName"`
	Kind           string                `bigquery:"kind"`
	State          string                `bigquery:"state"`
	URL            string                `bigquery:"url"`
	TransitionTime bigquery.NullDateTime `bigquery:"transitionTime"`
	Retries        bigquery.NullInt64    `bigquery:"retries"`
	UpgradesFrom   string                `bigquery:"upgradesFrom"`
	UpgradesTo     string                `bigquery:"upgradesTo"`
	Upgrade        bool                  `bigquery:"upgrade"`
}
