package onboard

import (
	"fmt"
	"path"
	"path/filepath"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
)

type KubeClientGetter func() (ctrlruntimeclient.Client, error)

const (
	BuildFarm                  = "build-farm"
	BuildUFarm                 = "build_farm"
	CI                         = "ci"
	CIOperator                 = "ci-operator"
	ClusterDisplay             = "cluster-display"
	ConfigUpdater              = "config-updater"
	GithubLdapUserGroupCreator = "github-ldap-user-group-creator"
	Master                     = "master"
	PodScaler                  = "pod-scaler"
	PromotedImageGovernor      = "promoted-image-governor"
	dexManifests               = "clusters/app.ci/dex/manifests.yaml"
)

func ServiceAccountKubeconfigPath(serviceAccount, clusterName string) string {
	return ServiceAccountFile(serviceAccount, clusterName, "config")
}

func ServiceAccountFile(serviceAccount, clusterName, fileType string) string {
	return fmt.Sprintf("sa.%s.%s.%s", serviceAccount, clusterName, fileType)
}

func ServiceAccountTokenFile(serviceAccount, clusterName string) string {
	return ServiceAccountFile(serviceAccount, clusterName, "token.txt")
}

func RepoMetadata() *api.Metadata {
	return &api.Metadata{
		Org:    "openshift",
		Repo:   "release",
		Branch: "master",
	}
}

func BuildFarmDirFor(releaseRepo, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", clusterName)
}

// The openshift-install places the first kubeconfig in ${installation_directory}/auth/kubeconfig
func AdminKubeconfig(installBase string) string {
	return path.Join(installBase, "/ocp-install-base/auth/kubeconfig")
}

func OAuthTemplatePath(releaseRepo, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", clusterName, "assets/admin_cluster_oauth_template.yaml")
}

func QuayioPullThroughCacheManifestPath(releaseRepo, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", clusterName, "assets/quayio-pull-through-cache-icsp.yaml")
}

func CertificateManifestPath(releaseRepo, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", clusterName, "cert-manager/certificate.yaml")
}

func ClusterInstallPath(releaseRepo string) string {
	return path.Join(releaseRepo, "clusters", "_cluster-install")
}

func BuildClustersPath(releaseRepo string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", "_cluster-init.yaml")
}

func CISchedulingWebhookManifestsPath(releaseRepo, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", clusterName, "ci-scheduling-webhook")
}

func CISchedulingWebhookManifestsCommonPath(releaseRepo, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", clusterName, "ci-scheduling-webhook/common")
}

func CISchedulingWebhookCommonPath(releaseRepo string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters/common_ci_scheduling_webhook")
}

func CISchedulingWebhookDNSPath(manifests string) string {
	return filepath.Join(manifests, "dns.yaml")
}
