package load

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"

	"github.com/openshift/ci-tools/pkg/api"
)

// ResolverInfo contains the data needed to get a config from the configresolver
type ResolverInfo struct {
	Address string
	Org     string
	Repo    string
	Branch  string
	// Variant is optional
	Variant string
}

func Config(path string, info *ResolverInfo) (*api.ReleaseBuildConfiguration, error) {
	// Load the standard configuration from the configresolver, path, or env
	var raw string
	if info != nil {
		return configFromResolver(info)
	} else if len(path) > 0 {
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

func configFromResolver(info *ResolverInfo) (*api.ReleaseBuildConfiguration, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/config", info.Address), nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to create request for configresolver: %s", err)
	}
	query := req.URL.Query()
	query.Add("org", info.Org)
	query.Add("repo", info.Repo)
	query.Add("branch", info.Branch)
	if len(info.Variant) > 0 {
		query.Add("variant", info.Variant)
	}
	req.URL.RawQuery = query.Encode()
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Failed to make request to configresolver: %s", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Response from configresolver != %d", http.StatusOK)
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Failed to read configresolver response body: %s", err)
	}
	configSpecHTTP := &api.ReleaseBuildConfiguration{}
	err = json.Unmarshal(data, configSpecHTTP)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config from configresolver: invalid configuration: %v\nvalue:\n%s", err, string(data))
	}
	return configSpecHTTP, nil
}

// Registry takes the path to a registry config directory and returns the full set of references, chains,
// and workflows that the registry's Resolver needs to resolve a user's MultiStageTestConfiguration
func Registry(root string) (references map[string]api.LiteralTestStep, chains map[string][]api.TestStep, workflows map[string]api.MultiStageTestConfiguration, err error) {
	references = map[string]api.LiteralTestStep{}
	chains = map[string][]api.TestStep{}
	workflows = map[string]api.MultiStageTestConfiguration{}
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info != nil && !info.IsDir() {
			bytes, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			dir := filepath.Dir(path)
			relpath, err := filepath.Rel(root, path)
			if err != nil {
				return fmt.Errorf("Failed to determine relative path for %s: %v", path, err)
			}
			prefix := strings.ReplaceAll(filepath.Dir(relpath), "/", "-")
			// Verify that file prefix is correct based on directory path
			if !strings.HasPrefix(filepath.Base(relpath), prefix) {
				return fmt.Errorf("File %s has incorrect prefix. Prefix should be %s", path, prefix)
			}
			if strings.HasSuffix(path, "-ref.yaml") {
				name, ref, err := loadReference(bytes, dir, prefix)
				if err != nil {
					return fmt.Errorf("Failed to load registry file %s: %v", path, err)
				}
				if name != prefix {
					return fmt.Errorf("name of reference in file %s should be %s", path, prefix)
				}
				references[name] = ref
			} else if strings.HasSuffix(path, "-chain.yaml") {
				name, chain, err := loadChain(bytes)
				if err != nil {
					return fmt.Errorf("Failed to load registry file %s: %v", path, err)
				}
				if name != prefix {
					return fmt.Errorf("name of chain in file %s should be %s", path, prefix)
				}
				chains[name] = chain
			} else if strings.HasSuffix(path, "-workflow.yaml") {
				name, workflow, err := loadWorkflow(bytes)
				if err != nil {
					return fmt.Errorf("Failed to load registry file %s: %v", path, err)
				}
				if name != prefix {
					return fmt.Errorf("name of workflow in file %s should be %s", path, prefix)
				}
				workflows[name] = workflow
			} else if strings.HasSuffix(path, "-commands.sh") || filepath.Base(path) == "OWNERS" || filepath.Ext(path) == ".md" {
				// these are files that are allowed to be in the registry, but aren't loaded; ignore
			} else {
				return fmt.Errorf("invalid file name: %s", path)
			}
		}
		return nil
	})
	return references, chains, workflows, err
}

func loadReference(bytes []byte, baseDir, prefix string) (string, api.LiteralTestStep, error) {
	step := api.RegistryReferenceConfig{}
	err := yaml.Unmarshal(bytes, &step)
	if err != nil {
		return "", api.LiteralTestStep{}, err
	}
	if step.Reference.Commands != fmt.Sprintf("%s-commands.sh", prefix) {
		return "", api.LiteralTestStep{}, fmt.Errorf("Reference %s has invalid command file path; command should be set to %s", step.Reference.As, fmt.Sprintf("%s-commands.sh", prefix))
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
	if workflow.Workflow.Steps.Workflow != nil {
		return "", api.MultiStageTestConfiguration{}, errors.New("workflows cannot contain other workflows")
	}
	return workflow.Workflow.As, workflow.Workflow.Steps, nil
}
