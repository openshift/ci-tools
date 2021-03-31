package load

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/util/gzip"
	"github.com/openshift/ci-tools/pkg/validation"
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
	RefSuffix      = "-ref.yaml"
	ChainSuffix    = "-chain.yaml"
	WorkflowSuffix = "-workflow.yaml"
	ObserverSuffix = "-observer.yaml"
	CommandsSuffix = "-commands.sh"
	MetadataSuffix = ".metadata.json"
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
	lock := &sync.Mutex{}
	errGroup := &errgroup.Group{}

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
		errGroup.Go(func() error {
			ext := filepath.Ext(path)
			if !info.IsDir() && (ext == ".yml" || ext == ".yaml") {
				configSpec, err := Config(path, "", "", nil)
				if err != nil {
					return fmt.Errorf("failed to load ci-operator config (%w)", err)
				}

				if err := validation.IsValidRuntimeConfiguration(configSpec); err != nil {
					return fmt.Errorf("invalid ci-operator config: %w", err)
				}
				logrus.Tracef("Adding %s to filenameToConfig", filepath.Base(path))
				lock.Lock()
				configs[filepath.Base(path)] = *configSpec
				lock.Unlock()
			}
			return nil
		})
		return nil
	})

	return configs, utilerrors.NewAggregate([]error{err, errGroup.Wait()})
}

func Config(path, unresolvedPath, registryPath string, info *ResolverInfo) (*api.ReleaseBuildConfiguration, error) {
	// Load the standard configuration path, env, or configresolver (in that order of priority)
	var raw string

	configSpecEnv, configSpecSet := os.LookupEnv("CONFIG_SPEC")
	unresolvedConfigEnv, unresolvedConfigSet := os.LookupEnv("UNRESOLVED_CONFIG")

	switch {
	case len(path) > 0:
		data, err := gzip.ReadFileMaybeGZIP(path)
		if err != nil {
			return nil, fmt.Errorf("--config error: %w", err)
		}
		raw = string(data)
	case configSpecSet:
		if len(configSpecEnv) == 0 {
			return nil, errors.New("CONFIG_SPEC environment variable cannot be set to an empty string")
		}
		raw = configSpecEnv
	case len(unresolvedPath) > 0:
		data, err := gzip.ReadFileMaybeGZIP(unresolvedPath)
		if err != nil {
			return nil, fmt.Errorf("--unresolved-config error: %w", err)
		}
		configSpec, err := literalConfigFromResolver(data, info.Address)
		err = results.ForReason("config_resolver_literal").ForError(err)
		return configSpec, err
	case unresolvedConfigSet:
		configSpec, err := literalConfigFromResolver([]byte(unresolvedConfigEnv), info.Address)
		err = results.ForReason("config_resolver_literal").ForError(err)
		return configSpec, err
	default:
		configSpec, err := configFromResolver(info)
		err = results.ForReason("config_resolver").ForError(err)
		return configSpec, err
	}
	configSpec := api.ReleaseBuildConfiguration{}
	if err := yaml.UnmarshalStrict([]byte(raw), &configSpec); err != nil {
		if len(path) > 0 {
			return nil, fmt.Errorf("invalid configuration in file %s: %w\nvalue:\n%s", path, err, raw)
		}
		return nil, fmt.Errorf("invalid configuration: %w\nvalue:\n%s", err, raw)
	}
	if registryPath != "" {
		refs, chains, workflows, _, _, observers, err := Registry(registryPath, false)
		if err != nil {
			return nil, fmt.Errorf("failed to load registry: %w", err)
		}
		configSpec, err = registry.ResolveConfig(registry.NewResolver(refs, chains, workflows, observers), configSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve configuration: %w", err)
		}
	}
	return &configSpec, nil
}

func configFromResolver(info *ResolverInfo) (*api.ReleaseBuildConfiguration, error) {
	identifier := fmt.Sprintf("%s/%s@%s", info.Org, info.Repo, info.Branch)
	if info.Variant != "" {
		identifier = fmt.Sprintf("%s [%s]", identifier, info.Variant)
	}
	logrus.Infof("Loading configuration from %s for %s", info.Address, identifier)
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/config", info.Address), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for configresolver: %w", err)
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
		return nil, fmt.Errorf("failed to make request to configresolver: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var responseBody string
		if data, err := ioutil.ReadAll(resp.Body); err != nil {
			logrus.WithError(err).Warn("Failed to read response body from configresolver.")
		} else {
			responseBody = string(data)
		}
		return nil, fmt.Errorf("got unexpected http %d status code from configresolver: %s", resp.StatusCode, responseBody)
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read configresolver response body: %w", err)
	}
	configSpecHTTP := &api.ReleaseBuildConfiguration{}
	err = json.Unmarshal(data, configSpecHTTP)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config from configresolver: invalid configuration: %w\nvalue:\n%s", err, string(data))
	}
	return configSpecHTTP, nil
}

func literalConfigFromResolver(raw []byte, address string) (*api.ReleaseBuildConfiguration, error) {
	// check that the user has sent us something reasonable
	unresolvedConfig := &api.ReleaseBuildConfiguration{}
	if err := yaml.UnmarshalStrict(raw, unresolvedConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal unresolved config: invalid configuration: %w, raw: %v", err, string(raw))
	}
	encoded, err := json.Marshal(unresolvedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal unresolved config: invalid configuration: %w", err)
	}
	resp, err := http.Post(fmt.Sprintf("%s/resolve", address), "application/json", bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("failed to request resolved config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var responseBody string
		if data, err := ioutil.ReadAll(resp.Body); err != nil {
			logrus.WithError(err).Warn("Failed to read response body from configresolver.")
		} else {
			responseBody = string(data)
		}
		return nil, fmt.Errorf("got unexpected http %d status code from configresolver: %s", resp.StatusCode, responseBody)
	}
	resolved, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read configresolver response body: %w", err)
	}
	resolvedConfig := &api.ReleaseBuildConfiguration{}
	if err = json.Unmarshal(resolved, resolvedConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resolved config: invalid configuration: %w\n", err)
	}
	return resolvedConfig, nil
}

// Registry takes the path to a registry config directory and returns the full set of references, chains,
// and workflows that the registry's Resolver needs to resolve a user's MultiStageTestConfiguration
func Registry(root string, flat bool) (registry.ReferenceByName, registry.ChainByName, registry.WorkflowByName, map[string]string, api.RegistryMetadata, registry.ObserverByName, error) {
	references := registry.ReferenceByName{}
	chains := registry.ChainByName{}
	workflows := registry.WorkflowByName{}
	observers := registry.ObserverByName{}
	documentation := map[string]string{}
	metadata := api.RegistryMetadata{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
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
			raw, err := gzip.ReadFileMaybeGZIP(path)
			if err != nil {
				return err
			}
			dir := filepath.Dir(path)
			var prefix string
			if !flat {
				relpath, err := filepath.Rel(root, path)
				if err != nil {
					return fmt.Errorf("failed to determine relative path for %s: %w", path, err)
				}
				prefix = strings.ReplaceAll(filepath.Dir(relpath), "/", "-")
				// Verify that file prefix is correct based on directory path
				if !strings.HasPrefix(filepath.Base(relpath), prefix) {
					return fmt.Errorf("file %s has incorrect prefix. Prefix should be %s", path, prefix)
				}
			}
			if strings.HasSuffix(path, RefSuffix) {
				name, doc, ref, err := loadReference(raw, dir, prefix, flat)
				if err != nil {
					return fmt.Errorf("failed to load registry file %s: %w", path, err)
				}
				if !flat && name != prefix {
					return fmt.Errorf("name of reference in file %s should be %s", path, prefix)
				}
				if strings.TrimSuffix(filepath.Base(path), RefSuffix) != name {
					return fmt.Errorf("filename %s does not match name of reference; filename should be %s", filepath.Base(path), fmt.Sprint(prefix, RefSuffix))
				}
				references[name] = ref
				documentation[name] = doc
			} else if strings.HasSuffix(path, ChainSuffix) {
				var chain api.RegistryChainConfig
				err := yaml.UnmarshalStrict(raw, &chain)
				if err != nil {
					return fmt.Errorf("failed to load registry file %s: %w", path, err)
				}
				if !flat && chain.Chain.As != prefix {
					return fmt.Errorf("name of chain in file %s should be %s", path, prefix)
				}
				if strings.TrimSuffix(filepath.Base(path), ChainSuffix) != chain.Chain.As {
					return fmt.Errorf("filename %s does not match name of chain; filename should be %s", filepath.Base(path), fmt.Sprint(prefix, ChainSuffix))
				}
				documentation[chain.Chain.As] = chain.Chain.Documentation
				chain.Chain.Documentation = ""
				chains[chain.Chain.As] = chain.Chain
			} else if strings.HasSuffix(path, WorkflowSuffix) {
				name, doc, workflow, err := loadWorkflow(raw)
				if err != nil {
					return fmt.Errorf("failed to load registry file %s: %w", path, err)
				}
				if !flat && name != prefix {
					return fmt.Errorf("name of workflow in file %s should be %s", path, prefix)
				}
				if strings.TrimSuffix(filepath.Base(path), WorkflowSuffix) != name {
					return fmt.Errorf("filename %s does not match name of workflow; filename should be %s", filepath.Base(path), fmt.Sprint(prefix, WorkflowSuffix))
				}
				workflows[name] = workflow
				documentation[name] = doc
			} else if strings.HasSuffix(path, MetadataSuffix) {
				var data api.RegistryInfo
				err := json.Unmarshal(raw, &data)
				if err != nil {
					return fmt.Errorf("failed to load metadata file %s: %w", path, err)
				}
				metadata[filepath.Base(data.Path)] = data
			} else if strings.HasSuffix(path, ObserverSuffix) {
				var observer api.RegistryObserverConfig
				err := yaml.UnmarshalStrict(raw, &observer)
				if err != nil {
					return fmt.Errorf("failed to load registry file %s: %w", path, err)
				}
				if !flat && observer.Observer.Name != prefix {
					return fmt.Errorf("name of observer in file %s should be %s", path, prefix)
				}
				if strings.TrimSuffix(filepath.Base(path), ObserverSuffix) != observer.Observer.Name {
					return fmt.Errorf("filename %s does not match name of chain; filename should be %s", filepath.Base(path), fmt.Sprint(prefix, ObserverSuffix))
				}
				if !flat && observer.Observer.Commands != fmt.Sprintf("%s%s", prefix, CommandsSuffix) {
					return fmt.Errorf("observer %s has invalid command file path; command should be set to %s", observer.Observer.Name, fmt.Sprintf("%s%s", prefix, CommandsSuffix))
				}
				command, err := gzip.ReadFileMaybeGZIP(filepath.Join(dir, observer.Observer.Commands))
				if err != nil {
					return err
				}
				observer.Observer.Commands = string(command)
				documentation[observer.Observer.Name] = observer.Observer.Documentation
				observer.Observer.Documentation = ""
				observers[observer.Observer.Name] = observer.Observer.Observer
			} else if strings.HasSuffix(path, CommandsSuffix) {
				// ignore
			} else if filepath.Base(path) == config.ConfigVersionFileName {
				if version, err := gzip.ReadFileMaybeGZIP(path); err == nil {
					logrus.WithField("version", string(version)).Info("Resolved configuration version")
				}
			} else {
				return fmt.Errorf("invalid file name: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	// create graph to verify that there are no cycles
	if _, err = registry.NewGraph(references, chains, workflows); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	err = registry.Validate(references, chains, workflows, observers)
	return references, chains, workflows, documentation, metadata, observers, err
}

func loadReference(bytes []byte, baseDir, prefix string, flat bool) (string, string, api.LiteralTestStep, error) {
	step := api.RegistryReferenceConfig{}
	err := yaml.UnmarshalStrict(bytes, &step)
	if err != nil {
		return "", "", api.LiteralTestStep{}, err
	}
	if !flat && step.Reference.Commands != fmt.Sprintf("%s%s", prefix, CommandsSuffix) {
		return "", "", api.LiteralTestStep{}, fmt.Errorf("reference %s has invalid command file path; command should be set to %s", step.Reference.As, fmt.Sprintf("%s%s", prefix, CommandsSuffix))
	}
	command, err := gzip.ReadFileMaybeGZIP(filepath.Join(baseDir, step.Reference.Commands))
	if err != nil {
		return "", "", api.LiteralTestStep{}, err
	}
	step.Reference.Commands = string(command)
	return step.Reference.As, step.Reference.Documentation, step.Reference.LiteralTestStep, nil
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
