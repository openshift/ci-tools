package main

import (
	"flag"
	"fmt"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"k8s.io/api/core/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"os"
	"path/filepath"
	"sigs.k8s.io/yaml"
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
	SaConfigUpdater      = "sa.config-updater"
	Config               = "config"
	Etc                  = "etc"
	CiSecretBootstrap    = "ci-secret-bootstrap"
	PerCiSecGen          = "periodic-ci-secret-generator"
	PerCiSecBoot         = "periodic-ci-secret-bootstrap"
	BuildClusters        = "build-clusters"
	Common               = "common"
	CommonExceptAppCi    = "common_except_app.ci"
)

func loadConfig(filename string, o interface{}) {
	data, err := ioutil.ReadFile(filename)
	check(err, "cannot open config file: ", filename)
	err = yaml.Unmarshal(data, o)
	check(err, "cannot unmarshall config file: ", filename)
}

func saveConfig(filename string, o interface{}) {
	y, err := yaml.Marshal(o)
	check(err, "cannot marshal InfraPeriodics")

	err = ioutil.WriteFile(filename, y, 0644)
	check(err, "cannot write InfraPeriodics file: ", filename)

}

type InfraPeriodics struct {
	Periodics []prowconfig.Periodic `json:"periodics,omitempty"`
}

func findPeriodic(ip *InfraPeriodics, name string) (*prowconfig.Periodic, error) {
	idx := -1
	for i, p := range ip.Periodics {
		if name == p.Name {
			idx = i
		}
	}
	if idx != -1 {
		return &ip.Periodics[idx], nil
	}
	return &prowconfig.Periodic{}, fmt.Errorf("couldn't find periodic with name: %s", name)
}

func findContainer(ps *v1.PodSpec, name string) (*v1.Container, error) {
	idx := -1
	for i, c := range ps.Containers {
		if c.Name == name {
			idx = i
		}
	}
	if idx != -1 {
		return &ps.Containers[idx], nil
	}
	return &v1.Container{}, fmt.Errorf("couldn't find Container with name: %s", name)
}

func findEnv(c *v1.Container, name string) (*v1.EnvVar, error) {
	idx := -1
	for i, e := range c.Env {
		if e.Name == name {
			idx = i
			break
		}
	}
	if idx != -1 {
		return &c.Env[idx], nil
	}
	return &v1.EnvVar{}, fmt.Errorf("couldn't find Env with name: %s", name)
}

func findVolume(ps *v1.PodSpec, name string) (*v1.Volume, error) {
	idx := -1
	for i, v := range ps.Volumes {
		if v.Name == name {
			idx = i
			break
		}
	}
	if idx != -1 {
		return &ps.Volumes[idx], nil
	}
	return &v1.Volume{}, fmt.Errorf("couldn't find Volume with name: %s", name)
}

func appendNewClustersConfigUpdaterToKubeconfig(per *prowconfig.Periodic, containerName string, clusterName string) {
	container, err := findContainer(per.Spec, containerName)
	if err != nil {
		logrus.WithError(err).Fatal()
	}
	env, err := findEnv(container, Kubeconfig)
	check(err)
	s := fmt.Sprintf(":/%s/%s/%s.%s.%s", Etc, BuildFarmCredentials, SaConfigUpdater, clusterName, Config)
	env.Value = env.Value + s
}

func appendBuildFarmCredentialSecret(per *prowconfig.Periodic, clusterName string) {
	v, err := findVolume(per.Spec, BuildFarmCredentials)
	check(err)
	configPath := fmt.Sprintf("%s.%s.%s", SaConfigUpdater, clusterName, Config)
	path := v1.KeyToPath{
		Key:  configPath,
		Path: configPath,
	}
	v.Secret.Items = append(v.Secret.Items, path)
}

type Post struct {
	OSRelease struct {
		Jobs []prowconfig.Postsubmit `json:"openshift/release,omitempty"`
	} `json:"postsubmits,omitempty"`
}

type Pre struct {
	OSRelease struct {
		Jobs []prowconfig.Presubmit `json:"openshift/release,omitempty"`
	} `json:"presubmits,omitempty"`
}

func main() {
	o := parseOptions()
	err := validateOptions(o)
	check(err, "Invalid arguments.")

	//TODO: probably a good idea to validate that this cluster doesn't exist

	updateInfraPeriodics(o)
	updatePostsubmits(o)
	updatePresubmits(o)
	//TODO: is the following good enough? it is hard to modify MD programmatically
	fmt.Printf("Please add information about the '%s' cluster to %s/clusters/README.md\n",
		o.clusterName, o.releaseRepo)
	initClusterBuildFarmDir(o, err)

}

func initClusterBuildFarmDir(o options, err error) {
	buildDir := filepath.Join(o.releaseRepo, Clusters, BuildClusters, o.buildFarmDir)
	err = os.MkdirAll(buildDir, 0777)
	check(err)

	commonLink := filepath.Join(o.releaseRepo, Clusters, BuildClusters, o.buildFarmDir, Common)
	createSymlink(fmt.Sprintf("../%s", Common), commonLink)

	commonExceptAppLink := filepath.Join(o.releaseRepo, Clusters, BuildClusters, o.buildFarmDir, CommonExceptAppCi)
	createSymlink(fmt.Sprintf("../%s", CommonExceptAppCi), commonExceptAppLink)
}

func createSymlink(target string, link string) {
	err := os.Symlink(target, link)
	check(err, "cannot create symlink.")
}

func updatePresubmits(o options) {
	presubmitsFile := filepath.Join(o.releaseRepo, CiOperator, Jobs, Openshift, Release, "openshift-release-master-presubmits.yaml")
	fmt.Printf("Updating Presubmit Jobs: %s\n", presubmitsFile)
	presubmits := &Pre{}
	loadConfig(presubmitsFile, presubmits)
	presubmit := GeneratePresubmit(o.clusterName, o.buildFarmDir)
	presubmits.OSRelease.Jobs = append(presubmits.OSRelease.Jobs, presubmit)
	saveConfig(presubmitsFile, presubmits)
}

func updatePostsubmits(o options) {
	postsubmitsFile := filepath.Join(o.releaseRepo, CiOperator, Jobs, Openshift, Release, "openshift-release-master-postsubmits.yaml")
	fmt.Printf("Updating Postsubmit Jobs: %s\n", postsubmitsFile)
	postsubmits := &Post{}
	loadConfig(postsubmitsFile, postsubmits)
	postsubmit := GeneratePostsubmit(o.clusterName, o.buildFarmDir)
	postsubmits.OSRelease.Jobs = append(postsubmits.OSRelease.Jobs, postsubmit)
	saveConfig(postsubmitsFile, *postsubmits)
}

func updateInfraPeriodics(o options) {
	ipFile := filepath.Join(o.releaseRepo, CiOperator, Jobs, InfraPeriodicsFile)
	fmt.Printf("Updating Periodic Jobs: %s\n", ipFile)
	ip := &InfraPeriodics{}
	loadConfig(ipFile, ip)

	rotSASecretsPer, err := findPeriodic(ip, PerRotSaSecs)
	check(err)

	appendNewClustersConfigUpdaterToKubeconfig(rotSASecretsPer, "", o.clusterName)
	appendBuildFarmCredentialSecret(rotSASecretsPer, o.clusterName)
	ap := GeneratePeriodic(o.clusterName, o.buildFarmDir)
	ip.Periodics = append(ip.Periodics, ap)

	for _, perName := range []string{PerCiSecGen, PerCiSecBoot} {
		per, err := findPeriodic(ip, perName)
		check(err)
		appendNewClustersConfigUpdaterToKubeconfig(per, CiSecretBootstrap, o.clusterName)
		appendBuildFarmCredentialSecret(per, o.clusterName)
	}

	saveConfig(ipFile, *ip)
}

func check(err error, args ...interface{}) {
	if err != nil {
		logrus.WithError(err).Fatal(args)
	}
}
