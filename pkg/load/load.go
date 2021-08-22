package load

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	"github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/util/gzip"
	"github.com/openshift/ci-tools/pkg/validation"
)

type RegistryFlag uint8

const (
	RefSuffix      = "-ref.yaml"
	ChainSuffix    = "-chain.yaml"
	WorkflowSuffix = "-workflow.yaml"
	ObserverSuffix = "-observer.yaml"
	CommandsSuffix = "-commands.sh"
	MetadataSuffix = ".metadata.json"
)

const (
	RegistryFlat = RegistryFlag(1) << iota
	RegistryMetadata
	RegistryDocumentation
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

	err := filepath.WalkDir(path, func(path string, info fs.DirEntry, err error) error {
		if info == nil || err != nil {
			// file may not exist due to race condition between the reload and k8s removing deleted/moved symlinks in a confimap directory; ignore it
			if os.IsNotExist(err) {
				return nil
			}
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
				configSpec, err := Config(path, "", "", nil, nil)
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

func Config(path, unresolvedPath, registryPath string, resolver server.ResolverClient, info *api.Metadata) (*api.ReleaseBuildConfiguration, error) {
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
		// if being run by pj-rehearse, config spec may be base64 and gzipped
		if decoded, err := base64.StdEncoding.DecodeString(configSpecEnv); err != nil {
			raw = configSpecEnv
		} else {
			data, err := gzip.ReadBytesMaybeGZIP(decoded)
			if err != nil {
				return nil, fmt.Errorf("--config error: %w", err)
			}
			raw = string(data)
		}
	case len(unresolvedPath) > 0:
		data, err := gzip.ReadFileMaybeGZIP(unresolvedPath)
		if err != nil {
			return nil, fmt.Errorf("--unresolved-config error: %w", err)
		}
		configSpec, err := resolver.Resolve(data)
		err = results.ForReason("config_resolver_literal").ForError(err)
		return configSpec, err
	case unresolvedConfigSet:
		configSpec, err := resolver.Resolve([]byte(unresolvedConfigEnv))
		err = results.ForReason("config_resolver_literal").ForError(err)
		return configSpec, err
	default:
		configSpec, err := resolver.Config(info)
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
		refs, chains, workflows, _, _, observers, err := Registry(registryPath, RegistryFlag(0))
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

// Registry takes the path to a registry config directory and returns the full set of references, chains,
// and workflows that the registry's Resolver needs to resolve a user's MultiStageTestConfiguration
func Registry(root string, flags RegistryFlag) (registry.ReferenceByName, registry.ChainByName, registry.WorkflowByName, map[string]string, api.RegistryMetadata, registry.ObserverByName, error) {
	flat := flags&RegistryFlat != 0
	references := registry.ReferenceByName{}
	chains := registry.ChainByName{}
	workflows := registry.WorkflowByName{}
	observers := registry.ObserverByName{}
	var documentation map[string]string
	var metadata api.RegistryMetadata
	if flags&RegistryDocumentation != 0 {
		documentation = map[string]string{}
	}
	if flags&RegistryMetadata != 0 {
		metadata = api.RegistryMetadata{}
	}
	err := filepath.WalkDir(root, func(path string, info fs.DirEntry, err error) error {
		if info != nil && strings.HasPrefix(info.Name(), "..") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err != nil {
			// file may not exist due to race condition between the reload and k8s removing deleted/moved symlinks in a confimap directory; ignore it
			if os.IsNotExist(err) {
				return nil
			}
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
				if documentation != nil {
					documentation[name] = doc
				}
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
				if documentation != nil {
					documentation[chain.Chain.As] = chain.Chain.Documentation
				}
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
				if documentation != nil {
					documentation[name] = doc
				}
			} else if strings.HasSuffix(path, MetadataSuffix) {
				if metadata == nil {
					return nil
				}
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
				if documentation != nil {
					documentation[observer.Observer.Name] = observer.Observer.Documentation
				}
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
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	// validate the integrity of each reference
	v := validation.NewValidator()
	var validationErrors []error
	for _, r := range references {
		if err := v.IsValidReference(r); err != nil {
			validationErrors = append(validationErrors, err...)
		}
	}
	if len(validationErrors) > 0 {
		return nil, nil, nil, nil, nil, nil, utilerrors.NewAggregate(validationErrors)
	}
	return references, chains, workflows, documentation, metadata, observers, nil
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
