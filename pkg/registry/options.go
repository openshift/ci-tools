package registry

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v2"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

type Options struct {
	resolverAddress      string
	org                  string
	repo                 string
	branch               string
	variant              string
	injectTest           string
	registryPath         string
	configSpecPath       string
	unresolvedConfigPath string
}

func (o Options) ResolverAddress() string {
	return o.resolverAddress
}

var (
	configResolverAddress = api.URLForService(api.ServiceConfig)
)

func (o *Options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.resolverAddress, "resolver-address", configResolverAddress, "Address of configresolver")
	fs.StringVar(&o.org, "org", "", "Org of the project (used by configresolver)")
	fs.StringVar(&o.repo, "repo", "", "Repo of the project (used by configresolver)")
	fs.StringVar(&o.branch, "branch", "", "Branch of the project (used by configresolver)")
	fs.StringVar(&o.variant, "variant", "", "Variant of the project's ci-operator config (used by configresolver)")
	fs.StringVar(&o.injectTest, "with-test-from", "", "Inject a test from another ci-operator config, specified by ORG/REPO@BRANCH{__VARIANT}:TEST or JSON (used by configresolver)")
	flag.StringVar(&o.registryPath, "registry", "", "Path to the step registry directory")
	flag.StringVar(&o.configSpecPath, "config", "", "The configuration file. If not specified the CONFIG_SPEC environment variable or the configresolver will be used.")
	flag.StringVar(&o.unresolvedConfigPath, "unresolved-config", "", "The configuration file, before resolution. If not specified the UNRESOLVED_CONFIG environment variable will be used, if set.")

}

func (o *Options) ResolveConfigSpec(jobSpec *api.JobSpec) (*api.ReleaseBuildConfiguration, error) {
	info := o.GetResolverInfo(jobSpec)
	resolverClient := server.NewResolverClient(o.resolverAddress)

	if o.unresolvedConfigPath != "" && o.configSpecPath != "" {
		return nil, errors.New("cannot set --config and --unresolved-config at the same time")
	}
	if o.unresolvedConfigPath != "" && o.ResolverAddress() == "" {
		return nil, errors.New("cannot request resolved config with --unresolved-config unless providing --resolver-address")
	}

	injectTest, err := o.GetInjectTest()
	if err != nil {
		return nil, err
	}

	var config *api.ReleaseBuildConfiguration
	if injectTest != nil {
		if o.ResolverAddress() == "" {
			return nil, errors.New("cannot request config with injected test without providing --resolver-address")
		}
		if o.unresolvedConfigPath != "" || o.configSpecPath != "" {
			return nil, errors.New("cannot request injecting test into locally provided config")
		}
		config, err = resolverClient.ConfigWithTest(info, injectTest, len(jobSpec.ExtraRefs) > 1)
	} else {
		config, err = o.loadConfig(info)
	}

	if err != nil {
		return nil, results.ForReason("loading_config").WithError(err).Errorf("failed to load configuration: %v", err)
	}

	return config, nil
}

func (o *Options) GetResolverInfo(jobSpec *api.JobSpec) *api.Metadata {
	// address and variant can only be set via options
	info := &api.Metadata{Variant: o.variant}

	allRefs := jobSpec.ExtraRefs
	if jobSpec.Refs != nil {
		allRefs = append([]prowapi.Refs{*jobSpec.Refs}, allRefs...)
	}

	// identify org, repo, and branch from refs object
	for _, ref := range allRefs {
		if ref.Org != "" && ref.Repo != "" && ref.BaseRef != "" {
			info.Org += fmt.Sprintf("%s,", ref.Org)
			info.Repo += fmt.Sprintf("%s,", ref.Repo)
			info.Branch += fmt.Sprintf("%s,", ref.BaseRef)
		}
	}
	info.Org = strings.TrimSuffix(info.Org, ",")
	info.Repo = strings.TrimSuffix(info.Repo, ",")
	info.Branch = strings.TrimSuffix(info.Branch, ",")

	// if flags set, override previous values
	if o.org != "" {
		info.Org = o.org
	}
	if o.repo != "" {
		info.Repo = o.repo
	}
	if o.branch != "" {
		info.Branch = o.branch
	}
	return info
}

func (o *Options) GetInjectTest() (*api.MetadataWithTest, error) {
	if o.injectTest == "" {
		return nil, nil
	}
	var ret api.MetadataWithTest
	if err := json.Unmarshal([]byte(o.injectTest), &ret); err == nil {
		return &ret, nil
	}

	return api.MetadataTestFromString(o.injectTest)
}

// loadConfig loads the standard configuration path, env, or configresolver (in that order of priority)
func (o *Options) loadConfig(info *api.Metadata) (*api.ReleaseBuildConfiguration, error) {
	var raw string

	resolverClient := server.NewResolverClient(o.resolverAddress)

	configSpecEnv, configSpecSet := os.LookupEnv("CONFIG_SPEC")
	unresolvedConfigEnv, unresolvedConfigSet := os.LookupEnv("UNRESOLVED_CONFIG")

	switch {
	case len(o.configSpecPath) > 0:
		data, err := gzip.ReadFileMaybeGZIP(o.configSpecPath)
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
	case len(o.unresolvedConfigPath) > 0:
		data, err := gzip.ReadFileMaybeGZIP(o.unresolvedConfigPath)
		if err != nil {
			return nil, fmt.Errorf("--unresolved-config error: %w", err)
		}
		configSpec, err := resolverClient.Resolve(data)
		err = results.ForReason("config_resolver_literal").ForError(err)
		return configSpec, err
	case unresolvedConfigSet:
		configSpec, err := resolverClient.Resolve([]byte(unresolvedConfigEnv))
		err = results.ForReason("config_resolver_literal").ForError(err)
		return configSpec, err
	default:
		configSpec, err := resolverClient.Config(info)
		err = results.ForReason("config_resolver").ForError(err)
		return configSpec, err
	}
	configSpec := api.ReleaseBuildConfiguration{}
	if err := yaml.UnmarshalStrict([]byte(raw), &configSpec); err != nil {
		if len(o.configSpecPath) > 0 {
			return nil, fmt.Errorf("invalid configuration in file %s: %w\nvalue:\n%s", o.configSpecPath, err, raw)
		}
		return nil, fmt.Errorf("invalid configuration: %w\nvalue:\n%s", err, raw)
	}
	if o.registryPath != "" {
		refs, chains, workflows, _, _, observers, err := Load(o.registryPath, RegistryFlag(0))
		if err != nil {
			return nil, fmt.Errorf("failed to load registry: %w", err)
		}
		configSpec, err = ResolveConfig(NewResolver(refs, chains, workflows, observers), configSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve configuration: %w", err)
		}
	}
	return &configSpec, nil
}
