package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/openshift/ci-operator-prowgen/pkg/diffs"
)

type options struct {
	configPath    string
	jobConfigPath string
}

func gatherOptions() options {
	o := options{}
	flag.StringVar(&o.configPath, "config-path", "/etc/config/config.yaml", "Path to Prow config.yaml")
	flag.StringVar(&o.jobConfigPath, "job-config-path", "", "Path to prow job configs.")
	flag.Parse()
	return o
}

func validateOptions(o options) error {
	if len(o.jobConfigPath) == 0 {
		return fmt.Errorf("empty --job-config-path")
	}
	return nil
}

func main() {
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		log.Fatal(err)
	}

	changedPresubmits, err := diffs.GetChangedPresubmits(o.configPath, o.jobConfigPath)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// just print them for now.
	for repo, jobs := range changedPresubmits {
		fmt.Println(repo)
		for _, job := range jobs {
			fmt.Println(job.Name)
		}
	}
}
