package rover

import (
	"time"

	"cloud.google.com/go/bigquery"
)

type UserItem struct {
	User
	Created time.Time
}

func (u *UserItem) Save() (map[string]bigquery.Value, string, error) {
	return map[string]bigquery.Value{
		"github_username": u.GitHubUsername,
		"uid":             u.UID,
		"cost_center":     u.CostCenter,
		"created":         u.Created,
	}, bigquery.NoDedupeID, nil
}
