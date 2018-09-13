package jobconfig

import (
	"fmt"
	"io/ioutil"

	"github.com/ghodss/yaml"

	prowconfig "k8s.io/test-infra/prow/config"
)

// ReadFromFile reads Prow job config from a YAML file
func ReadFromFile(path string) (*prowconfig.JobConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read Prow job config (%v)", err)
	}

	var jobConfig *prowconfig.JobConfig
	if err := yaml.Unmarshal(data, &jobConfig); err != nil {
		return nil, fmt.Errorf("failed to load Prow job config (%v)", err)
	}
	if jobConfig == nil { // happens when `data` is empty
		return nil, fmt.Errorf("failed to load Prow job config")
	}

	return jobConfig, nil
}

// WriteToFile writes Prow job config to a YAML file
func WriteToFile(path string, jobConfig *prowconfig.JobConfig) error {
	jobConfigAsYaml, err := yaml.Marshal(*jobConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal the job config (%v)", err)
	}
	if err := ioutil.WriteFile(path, jobConfigAsYaml, 0664); err != nil {
		return err
	}

	return nil
}
