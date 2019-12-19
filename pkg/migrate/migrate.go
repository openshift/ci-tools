package migrate

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	ProwClusterURL            = "https://api.ci.openshift.org"
	build01ClusterContextName = "ci/api-build01-ci-devcluster-openshift-com:6443"
)

var (
	migratedRepos = sets.NewString(
		"openshift/ci-secret-mirroring-controller/master",
		"openshift/origin/master",
	)
)

func Migrated(org, repo, branch string) bool {
	// gradually, we can add regex
	// eventually, we will return true without any check
	return migratedRepos.Has(fmt.Sprintf("%s/%s/%s", org, repo, branch))
}

func GetBuildClusterForPresubmit(org, repo, branch string) string {
	if Migrated(org, repo, branch) {
		return build01ClusterContextName
	}
	return ""
}

func GetBuildClusterForPostsubmit(org, repo, branch string) string {
	return ""
}

func GetBuildClusterForPeriodic(org, repo, branch string) string {
	return ""
}
