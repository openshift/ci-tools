package migrate

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	ProwClusterURL = "https://api.ci.openshift.org"
)

var (
	migratedRepos = sets.NewString(
		"openshift/origin/master",
	)
)

func Migrated(org, repo, branch string) bool {
	// gradually, we can add regex
	// eventually, we will return true without any check
	return migratedRepos.Has(fmt.Sprintf("%s/%s/%s", org, repo, branch))
}
