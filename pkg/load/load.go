package load

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"

	"github.com/openshift/ci-tools/pkg/api"
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
		return nil, fmt.Errorf("invalid configuration: %v\nvalue:\n%s", err, raw)
	}
	return configSpec, nil
}

// Registry takes the path to a registry config directory and returns the full set of references, chains,
// and workflows that the registry's Resolver needs to resolve a user's MultiStageTestConfiguration
func Registry(root string) (references map[string]api.LiteralTestStep, chains map[string][]api.TestStep, workflows map[string]api.MultiStageTestConfiguration, err error) {
	references = map[string]api.LiteralTestStep{}
	chains = map[string][]api.TestStep{}
	workflows = map[string]api.MultiStageTestConfiguration{}
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			bytes, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.HasSuffix(path, "ref.yaml") {
				name, ref, err := loadReference(bytes, filepath.Dir(path))
				if err != nil {
					return err
				}
				references[name] = ref
			} else if strings.HasSuffix(path, "chain.yaml") {
				name, chain, err := loadChain(bytes)
				if err != nil {
					return err
				}
				chains[name] = chain
			} else if strings.HasSuffix(path, "workflow.yaml") {
				name, workflow, err := loadWorkflow(bytes)
				if err != nil {
					return err
				}
				workflows[name] = workflow
			} else if strings.HasSuffix(path, "commands.sh") {
				// ignore
			} else {
				return fmt.Errorf("invalid file name: %s", path)
			}
		}
		return nil
	})
	return references, chains, workflows, nil
}

func loadReference(bytes []byte, baseDir string) (string, api.LiteralTestStep, error) {
	step := api.RegistryReferenceConfig{}
	err := yaml.Unmarshal(bytes, &step)
	if err != nil {
		return "", api.LiteralTestStep{}, err
	}
	command, err := ioutil.ReadFile(filepath.Join(baseDir, step.Reference.Commands))
	if err != nil {
		return "", api.LiteralTestStep{}, err
	}
	step.Reference.Commands = string(command)
	return step.Reference.As, step.Reference.LiteralTestStep, nil
}

func loadChain(bytes []byte) (string, []api.TestStep, error) {
	chain := api.RegistryChainConfig{}
	err := yaml.Unmarshal(bytes, &chain)
	if err != nil {
		return "", []api.TestStep{}, err
	}
	return chain.Chain.As, chain.Chain.Steps, nil
}

func loadWorkflow(bytes []byte) (string, api.MultiStageTestConfiguration, error) {
	workflow := api.RegistryWorkflowConfig{}
	err := yaml.Unmarshal(bytes, &workflow)
	if err != nil {
		return "", api.MultiStageTestConfiguration{}, err
	}
	return workflow.Workflow.As, workflow.Workflow.Steps, nil
}
