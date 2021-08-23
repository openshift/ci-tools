package main

import (
	"flag"
	"fmt"
	"github.com/sirupsen/logrus"
	"os"
	"path/filepath"
)

type options struct {
	clusterName  string
	releaseRepo  string
	buildFarmDir string

	//flagutil.GitHubOptions TODO: this will come in later I think...lets ignore github stuff for now
}

func (o options) String() string {
	return fmt.Sprintf("cluster-name: %s\nrelease-repo: %s\nbuild-farm-dir: %s",
		o.clusterName,
		o.releaseRepo,
		o.buildFarmDir)
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.clusterName, "cluster-name", "", "The name of the new cluster.")
	fs.StringVar(&o.releaseRepo, "release-repo", "", "Path to the root of the openshift/release repository.")
	fs.StringVar(&o.buildFarmDir, "build-farm-dir", "", "The name of the new build farm directory.")
	//o.AddFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("cannot parse args: ", os.Args[1:])
	}
	return o
}

func validateOptions(o options) error {
	if o.clusterName == "" {
		return fmt.Errorf("--cluster-name must be provided")
	}
	if o.releaseRepo == "" {
		return fmt.Errorf("--release-repo must be provided")
	}
	if o.buildFarmDir == "" {
		return fmt.Errorf("--build-farm-dir must be provided")
	}

	return nil
}

const (
	CiOperator           = "ci-operator"
	Jobs                 = "jobs"
	InfraPeriodicsFile   = "infra-periodics.yaml"
	Kubeconfig           = "KUBECONFIG"
	PerRotSaSecs         = "periodic-rotate-serviceaccount-secrets"
	BuildFarmCredentials = "build-farm-credentials"
	ConfigUpdater        = "config-updater"
	Config               = "config"
	Etc                  = "etc"
	CiSecretBootstrap    = "ci-secret-bootstrap"
	PerCiSecGen          = "periodic-ci-secret-generator"
	PerCiSecBoot         = "periodic-ci-secret-bootstrap"
	BuildClusters        = "build-clusters"
	Common               = "common"
	CommonExceptAppCi    = "common_except_app.ci"
	Sa                   = "sa"
	Test                 = "test"
)

func main() {
	o := parseOptions()
	err := validateOptions(o)
	check(err, "Invalid arguments.")

	//TODO: probably a good idea to validate that this cluster doesn't exist
	// i think we can use the presence of a build dir

	updateInfraPeriodics(o)
	updatePostsubmits(o)
	updatePresubmits(o)
	//TODO: is the following good enough? it is hard to modify MD programmatically
	fmt.Printf("Please add information about the '%s' cluster to %s/clusters/README.md\n",
		o.clusterName, o.releaseRepo)
	initClusterBuildFarmDir(o)
	updateCiSecretBootstrapConfig(o)
	updateSecretGenerator(o)
	updateSanitizeProwJobs(o)
}

func initClusterBuildFarmDir(o options) {
	buildDir := filepath.Join(o.releaseRepo, Clusters, BuildClusters, o.buildFarmDir)
	fmt.Printf("Creating build dir: %s\n", buildDir)
	err := os.MkdirAll(buildDir, 0777)
	check(err)

	commonLink := filepath.Join(o.releaseRepo, Clusters, BuildClusters, o.buildFarmDir, Common)
	createSymlink(fmt.Sprintf("../%s", Common), commonLink)

	commonExceptAppLink := filepath.Join(o.releaseRepo, Clusters, BuildClusters, o.buildFarmDir, CommonExceptAppCi)
	createSymlink(fmt.Sprintf("../%s", CommonExceptAppCi), commonExceptAppLink)
}

func createSymlink(target string, link string) {
	fmt.Printf("Creating symlink from: %s to: %s\n", link, target)
	err := os.Symlink(target, link)
	check(err, "cannot create symlink.")
}

func check(err error, args ...interface{}) {
	if err != nil {
		logrus.WithError(err).Fatal(args)
	}
}
