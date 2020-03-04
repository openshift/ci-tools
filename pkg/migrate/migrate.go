package migrate

import (
	"fmt"
	"regexp"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	ProwClusterURL = "https://api.ci.openshift.org"
)

var (
	migratedRepos   = sets.NewString()
	migratedRegexes []*regexp.Regexp
)

func init() {
	for _, migratedRepo := range migratedRepos.List() {
		migratedRegexes = append(migratedRegexes, regexp.MustCompile(migratedRepo))
	}
}

func Migrated(org, repo, branch string) bool {
	for _, regex := range migratedRegexes {
		if regex.MatchString(fmt.Sprintf("%s/%s/%s", org, repo, branch)) {
			return true
		}
	}
	return false
}
