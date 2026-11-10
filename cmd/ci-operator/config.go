package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowio "sigs.k8s.io/prow/pkg/io"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	configSpecVar       = "CONFIG_SPEC"
	configSpecGcsUrlVar = "CONFIG_SPEC_GCS_URL"
	unresolvedConfigVar = "UNRESOLVED_CONFIG"
)

type gcsFileReader interface {
	Read(filePath string) ([]byte, error)
}

type bucketReader struct {
	opener prowio.Opener
}

func (b bucketReader) Read(filePath string) ([]byte, error) {
	content, err := prowio.ReadContent(context.Background(), logrus.WithField("file", filePath), b.opener, filePath)
	return content, err
}

// loadConfig loads the standard configuration path, env, gcs bucket env, or configresolver (in that order of priority)
func (o *options) loadConfig(info *api.Metadata, gcsReader gcsFileReader) (*api.ReleaseBuildConfiguration, error) {
	var raw string

	configSpecEnv, configSpecSet := os.LookupEnv(configSpecVar)
	configSpecGCSEnv, configSpecGCSSet := os.LookupEnv(configSpecGcsUrlVar)
	unresolvedConfigEnv, unresolvedConfigSet := os.LookupEnv(unresolvedConfigVar)

	decodeAndUnzip := func(rawConfig string) (string, error) {
		// if being run by pj-rehearse, config spec may be base64 and gzipped
		if decoded, err := base64.StdEncoding.DecodeString(rawConfig); err != nil {
			return rawConfig, nil
		} else {
			data, err := gzip.ReadBytesMaybeGZIP(decoded)
			if err != nil {
				return "", fmt.Errorf("--config error: %w", err)
			}
			return string(data), nil
		}
	}
	switch {
	case len(o.configSpecPath) > 0:
		data, err := gzip.ReadFileMaybeGZIP(o.configSpecPath)
		if err != nil {
			return nil, fmt.Errorf("--config error: %w", err)
		}
		raw = string(data)
	case configSpecSet:
		if len(configSpecEnv) == 0 {
			return nil, fmt.Errorf("%s environment variable cannot be set to an empty string", configSpecVar)
		}
		var err error
		raw, err = decodeAndUnzip(configSpecEnv)
		if err != nil {
			return nil, err
		}
	case configSpecGCSSet:
		if len(configSpecGCSEnv) == 0 {
			return nil, fmt.Errorf("%s environment variable cannot be set to an empty string", configSpecGcsUrlVar)
		}
		content, err := gcsReader.Read(configSpecGCSEnv)
		if err != nil {
			logrus.WithError(err).Fatalf("Error reading %s", configSpecGCSEnv)
		}
		raw, err = decodeAndUnzip(string(content))
		if err != nil {
			return nil, err
		}
	case len(o.unresolvedConfigPath) > 0:
		data, err := gzip.ReadFileMaybeGZIP(o.unresolvedConfigPath)
		if err != nil {
			return nil, fmt.Errorf("--unresolved-config error: %w", err)
		}
		configSpec, err := o.resolverClient.Resolve(data)
		err = results.ForReason("config_resolver_literal").ForError(err)
		return configSpec, err
	case unresolvedConfigSet:
		configSpec, err := o.resolverClient.Resolve([]byte(unresolvedConfigEnv))
		err = results.ForReason("config_resolver_literal").ForError(err)
		return configSpec, err
	default:
		configSpec, err := o.resolverClient.Config(info)
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
		refs, chains, workflows, clusterProfiles, _, _, observers, err := load.Registry(o.registryPath, load.RegistryFlag(0))
		if err != nil {
			return nil, fmt.Errorf("failed to load registry: %w", err)
		}
		configSpec, err = registry.ResolveConfig(registry.NewResolver(refs, chains, workflows, observers, clusterProfiles), configSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve configuration: %w", err)
		}
	}
	return &configSpec, nil
}

func parseKeyValParams(input []string, paramType string) (map[string]string, error) {
	var validationErrors []error
	params := make(map[string]string)
	for _, param := range input {
		paramNameAndVal := strings.Split(param, "=")
		if len(paramNameAndVal) == 2 {
			params[strings.TrimSpace(paramNameAndVal[0])] = strings.TrimSpace(paramNameAndVal[1])
		} else {
			validationErrors = append(validationErrors, fmt.Errorf("could not parse %s: %s is not in the format key=value", paramType, param))
		}
	}

	if len(validationErrors) > 0 {
		return nil, utilerrors.NewAggregate(validationErrors)
	}

	return params, nil
}

func setDisablePodScalerFromTarget(o *options) {
	if o.jobSpec.Target == "" || o.jobSpec.Target == "all" {
		return
	}
	for _, test := range o.configSpec.Tests {
		if test.As == o.jobSpec.Target && test.DisablePodScaler {
			o.jobSpec.DisablePodScaler = true
			return
		}
	}
}

func handleTargetAdditionalSuffix(o *options) {
	if o.targetAdditionalSuffix == "" {
		return
	}
	o.jobSpec.TargetAdditionalSuffix = o.targetAdditionalSuffix
	for i, test := range o.configSpec.Tests {
		for j, target := range o.targets.values {
			if test.As == target {
				targetWithSuffix := fmt.Sprintf("%s-%s", test.As, o.targetAdditionalSuffix)
				o.configSpec.Tests[i].As = targetWithSuffix
				if j == 0 { //only set if it is the first target
					o.jobSpec.Target = targetWithSuffix
				}
				o.targets.values[j] = targetWithSuffix
				logrus.Debugf("added suffix to target, now: %s", test.As)
				break
			}
		}
	}
}

func overrideMultiStageParams(o *options) error {
	// see if there are any passed-in multi-stage parameters.
	if len(o.multiStageParamOverrides.values) == 0 {
		return nil
	}

	multiStageParams, err := parseKeyValParams(o.multiStageParamOverrides.values, "multi-stage-param")

	if err != nil {
		return err
	}

	// for any multi-stage tests, go ahead and inject the passed-in parameters. Note that parameters explicitly passed
	// in to ci-operator will take precedence.
	for _, test := range o.configSpec.Tests {
		if test.MultiStageTestConfigurationLiteral != nil {
			if test.MultiStageTestConfigurationLiteral.Environment == nil {
				test.MultiStageTestConfigurationLiteral.Environment = make(api.TestEnvironment)
			}

			for paramName, paramVal := range multiStageParams {
				valueWithoutQuotes := strings.Trim(paramVal, `"'`)
				test.MultiStageTestConfigurationLiteral.Environment[paramName] = valueWithoutQuotes
			}
		}
	}

	return nil
}

// applyEnvOverrides processes environment variables with override prefixes and applies them to the test configurations.
// It checks for environment variables that start with "MULTISTAGE_PARAM_OVERRIDE_" and applies them to the environment settings of each test.
func applyEnvOverrides(o *options) {
	for _, envVar := range os.Environ() {
		if !strings.HasPrefix(envVar, "MULTISTAGE_PARAM_OVERRIDE_") {
			continue
		}
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := parts[0], parts[1]
		for _, test := range o.configSpec.Tests {
			if test.MultiStageTestConfigurationLiteral != nil {
				if test.MultiStageTestConfigurationLiteral.Environment == nil {
					test.MultiStageTestConfigurationLiteral.Environment = make(api.TestEnvironment)
				}
				test.MultiStageTestConfigurationLiteral.Environment[key] = value
			}
		}
	}
}

func overrideTestStepDependencyParams(o *options) error {
	dependencyOverrideParams, err := parseKeyValParams(o.dependencyOverrides.values, "dependency-override-param")

	if err != nil {
		return err
	}

	// first see if there are any dependency overrides at the test level. This should really only happen with rehearsals.
	for _, test := range o.configSpec.Tests {
		if test.MultiStageTestConfigurationLiteral != nil {
			for dependencyName, pullspec := range test.MultiStageTestConfigurationLiteral.DependencyOverrides {
				overrideTestStepDependency(dependencyName, pullspec, &test.MultiStageTestConfigurationLiteral.Pre)
				overrideTestStepDependency(dependencyName, pullspec, &test.MultiStageTestConfigurationLiteral.Test)
				overrideTestStepDependency(dependencyName, pullspec, &test.MultiStageTestConfigurationLiteral.Post)
			}
		}
	}

	// dependency overrides specified as params to ci-operator always take precedence.
	for dependencyName, pullspec := range dependencyOverrideParams {
		for _, test := range o.configSpec.Tests {
			if test.MultiStageTestConfigurationLiteral != nil {
				overrideTestStepDependency(dependencyName, pullspec, &test.MultiStageTestConfigurationLiteral.Pre)
				overrideTestStepDependency(dependencyName, pullspec, &test.MultiStageTestConfigurationLiteral.Test)
				overrideTestStepDependency(dependencyName, pullspec, &test.MultiStageTestConfigurationLiteral.Post)
			}
		}
	}

	return nil
}

func overrideTestStepDependency(name string, value string, steps *[]api.LiteralTestStep) {
	for stepI, step := range *steps {
		for depI, dependency := range step.Dependencies {
			if strings.EqualFold(dependency.Env, name) {
				steps := *steps
				steps[stepI].Dependencies[depI].PullSpec = value
			}
		}
	}
}
