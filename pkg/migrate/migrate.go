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
	migratedRepos = sets.NewString(
		"openshift/jenkins-openshift-login-plugin/*",
		//find ./ci-operator/config -type d -depth 2 | head -n 30 | while read i; do echo "\"${i#./ci-operator/config/}/*\","; done
		"ostreedev/ostree/*",
		"openshift-priv/csi-external-attacher/*",
		"openshift-priv/cluster-api-provider-azure/*",
		"openshift-priv/cluster-update-keys/*",
		"openshift-priv/vertical-pod-autoscaler-operator/*",
		"openshift-priv/multus-cni/*",
		"openshift-priv/oauth-server/*",
		"openshift-priv/template-service-broker-operator/*",
		"openshift-priv/ci-experiment-origin/*",
		"openshift-priv/kubernetes-kube-storage-version-migrator/*",
		"openshift-priv/openshift-state-metrics/*",
		"openshift-priv/cluster-api-provider-baremetal/*",
		"openshift-priv/kube-state-metrics/*",
		"openshift-priv/dedicated-admin-operator/*",
		"openshift-priv/loki/*",
		"openshift-priv/cluster-capacity/*",
		"openshift-priv/cluster-version-operator/*",
		"openshift-priv/windows-machine-config-operator/*",
		"openshift-priv/operator-lifecycle-manager/*",
		"openshift-priv/presto/*",
		"openshift-priv/cluster-dns-operator/*",
		"openshift-priv/crd-schema-gen/*",
		"openshift-priv/operator-registry/*",
		"openshift-priv/oauth-proxy/*",
		"openshift-priv/cluster-nfd-operator/*",
		"openshift-priv/pagerduty-operator/*",
		"openshift-priv/descheduler/*",
		"openshift-priv/client-go/*",
		"openshift-priv/leader-elector/*",
		"openshift-priv/openshift-tuned/*",
	)
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
