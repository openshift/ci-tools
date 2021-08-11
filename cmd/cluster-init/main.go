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
	clusterName string
	releaseRepo string

	//flagutil.GitHubOptions TODO: this will come in later I think...lets ignore github stuff for now
}

func (o options) String() string {
	return fmt.Sprintf("cluster-name: %s\nrelease-repo: %s",
		o.clusterName,
		o.releaseRepo)
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.clusterName, "cluster-name", "", "The name of the new cluster.")
	fs.StringVar(&o.releaseRepo, "release-repo", "", "Path to the root of the openshift/release repository.")
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

	return nil
}

type InfraPeriodics struct {
	Periodics []prowconfig.Periodic `json:"periodics,omitempty"`
}

func findPeriodicIdx(ip *InfraPeriodics, name string) (int, error) {
	for i, p := range ip.Periodics {
		if name == p.Name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("couldn't find periodic with name: %s", name)
}

func findContainerIdx(ps *v1.PodSpec, name string) (int, error) {
	for i, c := range ps.Containers {
		if c.Name == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("couldn't find container with name: %s", name)
}

func findEnvIdx(c v1.Container, name string) (int, error) {
	for i, e := range c.Env {
		if e.Name == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("couldn't find Env with name: %s", name)
}

func loadInfraPeriodics(filename string) *InfraPeriodics {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		logrus.WithError(err).Fatal("cannot open config file: ", filename)
	}
	ip := InfraPeriodics{}
	err = yaml.Unmarshal(data, &ip)
	if err != nil {
		logrus.WithError(err).Fatal("cannot unmarshall config file: ", filename)
	}

	return &ip
}

func writeInfraPeriodics(filename string, ip InfraPeriodics) {
	y, err := yaml.Marshal(ip)
	if err != nil {
		logrus.WithError(err).Fatal("cannot marshal InfraPeriodics")
	}

	if err = ioutil.WriteFile(filename, y, 0644); err != nil {
		logrus.WithError(err).Fatal("cannot write InfraPeriodics file: ", filename)
	}

}

func main() {
	o := parseOptions()
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}

	ipFile := filepath.Join(o.releaseRepo, "ci-operator", "jobs", "infra-periodics.yaml")
	ip := loadInfraPeriodics(ipFile)
	per, err := findPeriodicIdx(ip, "periodic-rotate-serviceaccount-secrets")
	if err != nil {
		logrus.WithError(err).Fatal()
	}

	c, err := findContainerIdx(ip.Periodics[per].Spec, "")
	if err != nil {
		logrus.WithError(err).Fatal()
	}
	env, err := findEnvIdx(ip.Periodics[per].Spec.Containers[c], "KUBECONFIG")
	if err != nil {
		logrus.WithError(err).Fatal()
	}

	ip.Periodics[per].Spec.Containers[c].Env[env].Value = "i can change this line!"

	writeInfraPeriodics(ipFile, *ip)
}
