package jobrunaggregatorapi

import "cloud.google.com/go/bigquery"

type ReleaseRow struct {
	ReleaseTag string `bigquery:"releaseTag"`
	Phase      string `bigquery:"phase"`

	// ChangeLog contains a base64-encoded HTML changelog. Why is this a blob? Behind the scenes,
	// the release controller calls out into another pod to execute oc adm release info --change-log...,
	// which returns an MD document that is pre-processed and handed off to BlackFriday, and then
	// post-processed by the release controller.  After all of that, the result is written, directly,
	// to the HTML page as it renders. This is the best the release controller team could do without touching
	// many dependencies -- and it does accomplish the main goal of archiving release changelogs for
	// troubleshooting after they get garbage collected by the release controller.
	//
	// This change log contains a diff from this payload, to the last previously accepted payload.
	ChangeLog bigquery.NullString `bigquery:"changeLog"`
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
	UpgradesFrom   bigquery.NullString   `bigquery:"upgradesFrom"`
	UpgradesTo     bigquery.NullString   `bigquery:"upgradesTo"`
	Upgrade        bool                  `bigquery:"upgrade"`
}
