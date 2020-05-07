package load

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/results"
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

const (
	refSuffix      = "-ref.yaml"
	chainSuffix    = "-chain.yaml"
	workflowSuffix = "-workflow.yaml"
	commandsSuffix = "-commands.sh"
)

// ByOrgRepo maps org --> repo --> list of branched and variant configs
type ByOrgRepo map[string]map[string][]api.ReleaseBuildConfiguration

func FromPathByOrgRepo(path string) (ByOrgRepo, error) {
	byFilename, err := fromPath(path)
	if err != nil {
		return nil, err
	}

	return partitionByOrgRepo(byFilename), nil
}

func partitionByOrgRepo(byFilename filenameToConfig) ByOrgRepo {
	byOrgRepo := map[string]map[string][]api.ReleaseBuildConfiguration{}
	for _, configuration := range byFilename {
		org, repo := configuration.Metadata.Org, configuration.Metadata.Repo
		if _, exists := byOrgRepo[org]; !exists {
			byOrgRepo[org] = map[string][]api.ReleaseBuildConfiguration{}
		}
		if _, exists := byOrgRepo[org][repo]; !exists {
			byOrgRepo[org][repo] = []api.ReleaseBuildConfiguration{}
		}
		byOrgRepo[org][repo] = append(byOrgRepo[org][repo], configuration)
	}
	return byOrgRepo
}

// FilenameToConfig contains configs keyed by the file they were found in
type filenameToConfig map[string]api.ReleaseBuildConfiguration

// FromPath returns all configs found at or below the given path
func fromPath(path string) (filenameToConfig, error) {
	configs := filenameToConfig{}
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if info == nil || err != nil {
			return err
		}
		if strings.HasPrefix(info.Name(), "..") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if info != nil && !info.IsDir() && (ext == ".yml" || ext == ".yaml") {
			configSpec, err := Config(path, "", nil)
			if err != nil {
				return fmt.Errorf("failed to load ci-operator config (%v)", err)
			}

			if err := configSpec.ValidateAtRuntime(); err != nil {
				return fmt.Errorf("invalid ci-operator config: %v", err)
			}
			logrus.Tracef("Adding %s to filenameToConfig", filepath.Base(path))
			configs[filepath.Base(path)] = *configSpec
		}
		return nil
	})

	return configs, err
}

func Config(path, registryPath string, info *ResolverInfo) (*api.ReleaseBuildConfiguration, error) {
	// Load the standard configuration path, env, or configresolver (in that order of priority)
	var raw string
	if len(path) > 0 {
		data, err := ioutil.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("--config error: %v", err)
		}
		raw = string(data)
	} else if spec, ok := os.LookupEnv("CONFIG_SPEC"); ok {
		if len(spec) == 0 {
			return nil, errors.New("CONFIG_SPEC environment variable cannot be set to an empty string")
		}
		raw = spec
	} else {
		configSpec, err := configFromResolver(info)
		err = results.ForReason("config_resolver").ForError(err)
		return configSpec, err
	}
	configSpec := api.ReleaseBuildConfiguration{}
	if err := yaml.UnmarshalStrict([]byte(raw), &configSpec); err != nil {
		if len(path) > 0 {
			return nil, fmt.Errorf("invalid configuration in file %s: %v\nvalue:\n%s", path, err, raw)
		}
		return nil, fmt.Errorf("invalid configuration: %v\nvalue:\n%s", err, raw)
	}
	if registryPath != "" {
		refs, chains, workflows, _, err := Registry(registryPath, false)
		if err != nil {
			return nil, fmt.Errorf("failed to load registry: %v", err)
		}
		configSpec, err = registry.ResolveConfig(registry.NewResolver(refs, chains, workflows), configSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve configuration: %v", err)
		}
	}
	return &configSpec, nil
}

func configFromResolver(info *ResolverInfo) (*api.ReleaseBuildConfiguration, error) {
	identifier := fmt.Sprintf("%s/%s@%s", info.Org, info.Repo, info.Branch)
	if info.Variant != "" {
		identifier = fmt.Sprintf("%s [%s]", identifier, info.Variant)
	}
	log.Printf("Loading configuration from %s for %s", info.Address, identifier)
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
		return nil, fmt.Errorf("Response from configresolver == %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
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
func Registry(root string, flat bool) (references registry.ReferenceByName, chains registry.ChainByName, workflows registry.WorkflowByName, documentation map[string]string, err error) {
	references = registry.ReferenceByName{}
	chains = registry.ChainByName{}
	workflows = registry.WorkflowByName{}
	documentation = map[string]string{}
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if info != nil && strings.HasPrefix(info.Name(), "..") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err != nil {
			return err
		}
		if info != nil && !info.IsDir() {
			if filepath.Ext(info.Name()) == ".md" || info.Name() == "OWNERS" {
				return nil
			}
			bytes, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			dir := filepath.Dir(path)
			var prefix string
			if !flat {
				relpath, err := filepath.Rel(root, path)
				if err != nil {
					return fmt.Errorf("Failed to determine relative path for %s: %v", path, err)
				}
				prefix = strings.ReplaceAll(filepath.Dir(relpath), "/", "-")
				// Verify that file prefix is correct based on directory path
				if !strings.HasPrefix(filepath.Base(relpath), prefix) {
					return fmt.Errorf("File %s has incorrect prefix. Prefix should be %s", path, prefix)
				}
			}
			if strings.HasSuffix(path, refSuffix) {
				name, doc, ref, err := loadReference(bytes, dir, prefix, flat)
				if err != nil {
					return fmt.Errorf("Failed to load registry file %s: %v", path, err)
				}
				if !flat && name != prefix {
					return fmt.Errorf("name of reference in file %s should be %s", path, prefix)
				}
				if strings.TrimSuffix(filepath.Base(path), refSuffix) != name {
					return fmt.Errorf("filename %s does not match name of reference; filename should be %s", filepath.Base(path), fmt.Sprint(prefix, refSuffix))
				}
				references[name] = ref
				documentation[name] = doc
			} else if strings.HasSuffix(path, chainSuffix) {
				name, doc, chain, err := loadChain(bytes)
				if err != nil {
					return fmt.Errorf("Failed to load registry file %s: %v", path, err)
				}
				if !flat && name != prefix {
					return fmt.Errorf("name of chain in file %s should be %s", path, prefix)
				}
				if strings.TrimSuffix(filepath.Base(path), chainSuffix) != name {
					return fmt.Errorf("filename %s does not match name of chain; filename should be %s", filepath.Base(path), fmt.Sprint(prefix, chainSuffix))
				}
				chains[name] = chain
				documentation[name] = doc
			} else if strings.HasSuffix(path, workflowSuffix) {
				name, doc, workflow, err := loadWorkflow(bytes)
				if err != nil {
					return fmt.Errorf("Failed to load registry file %s: %v", path, err)
				}
				if !flat && name != prefix {
					return fmt.Errorf("name of workflow in file %s should be %s", path, prefix)
				}
				if strings.TrimSuffix(filepath.Base(path), workflowSuffix) != name {
					return fmt.Errorf("filename %s does not match name of workflow; filename should be %s", filepath.Base(path), fmt.Sprint(prefix, workflowSuffix))
				}
				workflows[name] = workflow
				documentation[name] = doc
			} else if strings.HasSuffix(path, commandsSuffix) {
				// ignore
			} else {
				return fmt.Errorf("invalid file name: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		return references, chains, workflows, documentation, err
	}
	// create graph to verify that there are no cycles
	_, err = registry.NewGraph(references, chains, workflows)
	return references, chains, workflows, documentation, err
}

func loadReference(bytes []byte, baseDir, prefix string, flat bool) (string, string, api.LiteralTestStep, error) {
	step := api.RegistryReferenceConfig{}
	err := yaml.UnmarshalStrict(bytes, &step)
	if err != nil {
		return "", "", api.LiteralTestStep{}, err
	}
	if !flat && step.Reference.Commands != fmt.Sprintf("%s%s", prefix, commandsSuffix) {
		return "", "", api.LiteralTestStep{}, fmt.Errorf("Reference %s has invalid command file path; command should be set to %s", step.Reference.As, fmt.Sprintf("%s%s", prefix, commandsSuffix))
	}
	command, err := ioutil.ReadFile(filepath.Join(baseDir, step.Reference.Commands))
	if err != nil {
		return "", "", api.LiteralTestStep{}, err
	}
	step.Reference.Commands = string(command)
	return step.Reference.As, step.Reference.Documentation, step.Reference.LiteralTestStep, nil
}

func loadChain(bytes []byte) (string, string, []api.TestStep, error) {
	chain := api.RegistryChainConfig{}
	err := yaml.UnmarshalStrict(bytes, &chain)
	if err != nil {
		return "", "", []api.TestStep{}, err
	}
	return chain.Chain.As, chain.Chain.Documentation, chain.Chain.Steps, nil
}

func loadWorkflow(bytes []byte) (string, string, api.MultiStageTestConfiguration, error) {
	workflow := api.RegistryWorkflowConfig{}
	err := yaml.UnmarshalStrict(bytes, &workflow)
	if err != nil {
		return "", "", api.MultiStageTestConfiguration{}, err
	}
	if workflow.Workflow.Steps.Workflow != nil {
		return "", "", api.MultiStageTestConfiguration{}, errors.New("workflows cannot contain other workflows")
	}
	return workflow.Workflow.As, workflow.Workflow.Documentation, workflow.Workflow.Steps, nil
}
