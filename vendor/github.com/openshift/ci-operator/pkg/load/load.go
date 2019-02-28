package load

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/ghodss/yaml"

	"github.com/openshift/ci-operator/pkg/api"
)

func Config(path string) (*api.ReleaseBuildConfiguration, error) {
	// Load the standard configuration from the path or env
	var raw string
	if len(path) > 0 {
		data, err := ioutil.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("--config error: %v", err)
		}
		raw = string(data)
	} else {
		var ok bool
		raw, ok = os.LookupEnv("CONFIG_SPEC")
		if !ok || len(raw) == 0 {
			return nil, fmt.Errorf("CONFIG_SPEC environment variable is not set or empty and no config file was set")
		}
	}
	configSpec := &api.ReleaseBuildConfiguration{}
	if err := yaml.Unmarshal([]byte(raw), configSpec); err != nil {
		return nil, fmt.Errorf("invalid configuration: %v\nvalue:\n%s", err, string(raw))
	}
	return configSpec, nil
}
