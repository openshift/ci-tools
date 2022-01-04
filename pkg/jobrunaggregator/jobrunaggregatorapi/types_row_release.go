package jobrunaggregatorapi

import (
	"time"
)

type ReleaseRow struct {
	// Phase contains the overall status of a payload: e.g. Ready, Accepted,
	// Rejected. We do not store Ready payloads in bigquery, as we only want
	// the release after it's "fully baked."
	Phase string `bigquery:"phase"`

	// Release contains the X.Y version of the payload, e.g. 4.8
	Release string `bigquery:"release"`

	// Stream contains the stream of the payload, e.g. nightly or ci
	Stream string `bigquery:"stream"`

	// Architecture contains the architecture for the release, e.g. amd64
	Architecture string `bigquery:"architecture"`

	// ReleaseTag contains the release version, e.g. 4.8.0-0.nightly-2021-10-28-013428.
	ReleaseTag string `bigquery:"releaseTag"`

	// ReleaseTime contains the time the release was created, i.e. the -YYYY-MM-DD-HHMMSS suffix
	// of 4.8.0-0.nightly-2021-10-28-013428.
	ReleaseTime time.Time `bigquery:"releaseTime"`

	// PreviousReleaseTag contains the previously accepted build, on which any
	// changelog is based from.
	PreviousReleaseTag string `bigquery:"previousReleaseTag"`

	// KubernetesVersion contains the kube version, i.e. 1.22.1.
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

	// CreatedAt contains a timestamp for when this record was created in BigQuery.
	CreatedAt time.Time `bigquery:"createdAt" json:"-"`
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

	// CreatedAt contains a timestamp for when this record was created in BigQuery.
	CreatedAt time.Time `bigquery:"createdAt" json:"-"`
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

	// CreatedAt contains a timestamp for when this record was created in BigQuery.
	CreatedAt time.Time `bigquery:"createdAt" json:"-"`
}

type ReleaseJobRunRow struct {
	// Name contains the Prow name of this job run.
	Name string `bigquery:"name"`

	// ReleaseTag is the OpenShift version, e.g. 4.8.0-0.nightly-2021-10-28-013428.
	ReleaseTag string `bigquery:"releaseTag"`

	// JobName contains the job name as known by the release controller --
	// this is a shortened form like "aws-serial"
	JobName string `bigquery:"jobName"`

	// Kind contains the job run kind, i.e. whether it's Blocking or Informing.
	Kind string `bigquery:"kind"`

	// State holds the overall status of the job, such as Failed.
	State string `bigquery:"state"`

	// URL contains a link to Prow.
	URL string `bigquery:"url"`

	// Transition time contains the transition time from the release
	// controller.
	TransitionTime time.Time `bigquery:"transitionTime"`

	// Retries contains the number of retries were performed on this job,
	// for this release tag.
	Retries int `bigquery:"retries"`

	// UpgradesFrom contains the source version when this job run is
	// an upgrade.
	UpgradesFrom string `bigquery:"upgradesFrom"`

	// UpgradesTo contains the target version when this job run is
	// an upgrade.
	UpgradesTo string `bigquery:"upgradesTo"`

	// Upgrade is a flag that indicates whether this job run was an upgrade or not.
	Upgrade bool `bigquery:"upgrade"`

	// CreatedAt contains a timestamp for when this record was created in BigQuery.
	CreatedAt time.Time `bigquery:"createdAt" json:"-"`
}
