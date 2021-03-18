package validation

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/robfig/cron.v2"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/openshift/ci-tools/pkg/api"
)

type testStage uint8

const (
	testStagePre testStage = iota
	testStageTest
	testStagePost
)

type context struct {
	fieldRoot  string
	env        api.TestEnvironment
	seen       sets.String
	leasesSeen sets.String
	releases   sets.String
}

func newContext(fieldRoot string, env api.TestEnvironment, releases sets.String) context {
	return context{
		fieldRoot:  fieldRoot,
		env:        env,
		seen:       sets.NewString(),
		leasesSeen: sets.NewString(),
		releases:   releases,
	}
}

func (c *context) forField(name string) context {
	ret := *c
	ret.fieldRoot = c.fieldRoot + name
	return ret
}

func validateTestStepConfiguration(fieldRoot string, input []api.TestStepConfiguration, release *api.ReleaseTagConfiguration, releases sets.String, resolved bool) []error {
	var validationErrors []error

	// check for test.As duplicates
	validationErrors = append(validationErrors, searchForTestDuplicates(input)...)

	for num, test := range input {
		fieldRootN := fmt.Sprintf("%s[%d]", fieldRoot, num)
		if len(test.As) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: is required", fieldRootN))
		} else if test.As == "images" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: should not be called 'images' because it gets confused with '[images]' target", fieldRootN))
		} else if strings.HasPrefix(test.As, string(api.PipelineImageStreamTagReferenceIndexImage)) {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: should begin with 'ci-index' because it gets confused with 'ci-index' and `ci-index-...` targets", fieldRootN))
		} else if len(validation.IsDNS1123Subdomain(test.As)) != 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: '%s' is not a valid Kubernetes object name", fieldRootN, test.As))
		}
		if hasCommands, hasSteps, hasLiteral := len(test.Commands) != 0, test.MultiStageTestConfiguration != nil, test.MultiStageTestConfigurationLiteral != nil; !hasCommands && !hasSteps && !hasLiteral {
			validationErrors = append(validationErrors, fmt.Errorf("%s: either `commands`, `steps`, or `literal_steps` should be set", fieldRootN))
		} else if hasCommands && (hasSteps || hasLiteral) || (hasSteps && hasLiteral) {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `commands`, `steps`, and `literal_steps` are mutually exclusive", fieldRootN))
		}

		if test.Postsubmit && test.Cron != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `cron` and `postsubmit` are mututally exclusive", fieldRootN))
		}
		if test.Postsubmit && test.Interval != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `interval` and `postsubmit` are mututally exclusive", fieldRootN))
		}

		if test.Cron != nil && test.Interval != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `interval` and `cron` cannot both be set", fieldRootN))
		}

		if test.Interval != nil {
			if _, err := time.ParseDuration(*test.Interval); err != nil {
				validationErrors = append(validationErrors, fmt.Errorf("%s: cannot parse interval: %w", fieldRootN, err))
			}
		}

		if test.Cron != nil {
			if _, err := cron.Parse(*test.Cron); err != nil {
				validationErrors = append(validationErrors, fmt.Errorf("%s: cannot parse cron: %w", fieldRootN, err))
			}
		}

		// Validate Secret/Secrets
		if test.Secret != nil && test.Secrets != nil {
			validationErrors = append(validationErrors, fmt.Errorf("test.Secret and test.Secrets cannot both be set"))
		}

		if test.Secret != nil {
			test.Secrets = append(test.Secrets, test.Secret)
		}

		seen := sets.NewString()
		for _, secret := range test.Secrets {
			// K8s object names must be valid DNS 1123 subdomains.
			if len(validation.IsDNS1123Subdomain(secret.Name)) != 0 {
				validationErrors = append(validationErrors, fmt.Errorf("%s.name: '%s' is not a valid Kubernetes object name", fieldRootN, secret.Name))
			}
			// Validate no duplicate secret names, then append to list of names.
			if seen.Has(secret.Name) {
				validationErrors = append(validationErrors, fmt.Errorf("duplicate secret name entries found for %s", secret.Name))
			}
			seen.Insert(secret.Name)

			// validate path only if name is passed
			if secret.MountPath != "" {
				if ok := filepath.IsAbs(secret.MountPath); !ok {
					validationErrors = append(validationErrors, fmt.Errorf("%s.path: '%s' secret mount path is not valid value, should be ^((\\/*)\\w+)+", fieldRootN, secret.MountPath))
				}
			}
		}

		validationErrors = append(validationErrors, validateTestConfigurationType(fieldRootN, test, release, releases, resolved)...)
	}
	return validationErrors
}

// validateTestStepDependencies ensures that users have referenced valid dependencies
func validateTestStepDependencies(config *api.ReleaseBuildConfiguration) []error {
	dependencyErrors := func(step api.LiteralTestStep, testIdx int, stageField, stepField string, stepIdx int) []error {
		var errs []error
		for dependencyIdx, dependency := range step.Dependencies {
			validationError := func(message string) error {
				return fmt.Errorf("tests[%d].%s.%s[%d].dependencies[%d]: cannot determine source for dependency %q - %s", testIdx, stageField, stepField, stepIdx, dependencyIdx, dependency.Name, message)
			}
			stream, name, explicit := config.DependencyParts(dependency)
			if link := api.LinkForImage(stream, name); link == nil {
				errs = append(errs, validationError("ensure the correct ImageStream name was provided"))
			}
			if explicit {
				// the user has asked us for something specific, and we can
				// do some best-effort analysis of that input to see if it's
				// possible that it will resolve at run-time. We could just
				// let the step graph fail when this input is used to run a
				// job, but this validation will catch things faster and be
				// overall more useful, so we do both.
				var releaseName string
				switch {
				case api.IsReleaseStream(stream):
					releaseName = api.ReleaseNameFrom(stream)
				case api.IsReleasePayloadStream(stream):
					releaseName = name
				}

				if releaseName != "" {
					implicitlyConfigured := (releaseName == api.InitialReleaseName || releaseName == api.LatestReleaseName) && config.InputConfiguration.ReleaseTagConfiguration != nil
					_, explicitlyConfigured := config.InputConfiguration.Releases[releaseName]
					if !(implicitlyConfigured || explicitlyConfigured) {
						errs = append(errs, validationError(fmt.Sprintf("this dependency requires a %q release, which is not configured", releaseName)))
					}
				}

				if stream == api.PipelineImageStream {
					switch name {
					case string(api.PipelineImageStreamTagReferenceRoot):
						if config.InputConfiguration.BuildRootImage == nil {
							errs = append(errs, validationError("this dependency requires a build root, which is not configured"))
						}
					case string(api.PipelineImageStreamTagReferenceSource):
						// always present
					case string(api.PipelineImageStreamTagReferenceBinaries):
						if config.BinaryBuildCommands == "" {
							errs = append(errs, validationError("this dependency requires built binaries, which are not configured"))
						}
					case string(api.PipelineImageStreamTagReferenceTestBinaries):
						if config.TestBinaryBuildCommands == "" {
							errs = append(errs, validationError("this dependency requires built test binaries, which are not configured"))
						}
					case string(api.PipelineImageStreamTagReferenceRPMs):
						if config.RpmBuildCommands == "" {
							errs = append(errs, validationError("this dependency requires built RPMs, which are not configured"))
						}
					case string(api.PipelineImageStreamTagReferenceIndexImage):
						if config.Operator == nil {
							errs = append(errs, validationError("this dependency requires an operator bundle configuration, which is not configured"))
						}
					default:
						// this could be a named index image
						if api.IsIndexImage(name) {
							if config.Operator != nil {
								foundBundle := false
								for _, bundle := range config.Operator.Bundles {
									if api.IndexName(bundle.As) == name {
										foundBundle = true
										break
									}
								}
								if !foundBundle {
									errs = append(errs, validationError(fmt.Sprintf("this dependency requires an operator bundle named %s, which is not configured", strings.TrimPrefix(name, string(api.PipelineImageStreamTagReferenceIndexImage)))))
								}
							} else {
								errs = append(errs, validationError("this dependency requires an operator bundle configuration, which is not configured"))
							}
							break
						}
						// this could be a base image, a project image, or a bundle image
						if !config.IsBaseImage(name) && !config.BuildsImage(name) && !config.IsBundleImage(name) {
							errs = append(errs, validationError("no base image import, project image build, or bundle image build is configured to provide this dependency"))
						}
					}
				}
			}
		}
		return errs
	}
	processSteps := func(steps []api.TestStep, testIdx int, stageField, stepField string) []error {
		var errs []error
		for stepIdx, test := range steps {
			if test.LiteralTestStep != nil {
				errs = append(errs, dependencyErrors(*test.LiteralTestStep, testIdx, stageField, stepField, stepIdx)...)
			}
		}
		return errs
	}
	processLiteralSteps := func(steps []api.LiteralTestStep, testIdx int, stageField, stepField string) []error {
		var errs []error
		for stepIdx, test := range steps {
			errs = append(errs, dependencyErrors(test, testIdx, stageField, stepField, stepIdx)...)
		}
		return errs
	}
	var errs []error
	for testIdx, test := range config.Tests {
		if test.MultiStageTestConfiguration != nil {
			for _, item := range []struct {
				field string
				list  []api.TestStep
			}{
				{field: "pre", list: test.MultiStageTestConfiguration.Pre},
				{field: "test", list: test.MultiStageTestConfiguration.Test},
				{field: "post", list: test.MultiStageTestConfiguration.Post},
			} {
				errs = append(errs, processSteps(item.list, testIdx, "steps", item.field)...)
			}
		}
		if test.MultiStageTestConfigurationLiteral != nil {
			for _, item := range []struct {
				field string
				list  []api.LiteralTestStep
			}{
				{field: "pre", list: test.MultiStageTestConfigurationLiteral.Pre},
				{field: "test", list: test.MultiStageTestConfigurationLiteral.Test},
				{field: "post", list: test.MultiStageTestConfigurationLiteral.Post},
			} {
				errs = append(errs, processLiteralSteps(item.list, testIdx, "literal_steps", item.field)...)
			}
		}
	}
	return errs
}

func validateClusterProfile(fieldRoot string, p api.ClusterProfile) []error {
	switch p {
	case api.ClusterProfileAWS,
		api.ClusterProfileAWSAtomic,
		api.ClusterProfileAWSCentos,
		api.ClusterProfileAWSCentos40,
		api.ClusterProfileAWSGluster,
		api.ClusterProfileAzure4,
		api.ClusterProfileAzureArc,
		api.ClusterProfileGCP,
		api.ClusterProfileGCP40,
		api.ClusterProfileGCPHA,
		api.ClusterProfileGCPCRIO,
		api.ClusterProfileGCPLogging,
		api.ClusterProfileGCPLoggingJournald,
		api.ClusterProfileGCPLoggingJSONFile,
		api.ClusterProfileGCPLoggingCRIO,
		api.ClusterProfileLibvirtPpc64le,
		api.ClusterProfileLibvirtS390x,
		api.ClusterProfileOpenStack,
		api.ClusterProfileOpenStackOsuosl,
		api.ClusterProfileOpenStackVexxhost,
		api.ClusterProfileOpenStackPpc64le,
		api.ClusterProfileOvirt,
		api.ClusterProfilePacket,
		api.ClusterProfileVSphere,
		api.ClusterProfileKubevirt,
		api.ClusterProfileAWSCPaaS,
		api.ClusterProfileOSDEphemeral:
		return nil
	}
	return []error{fmt.Errorf("%s: invalid cluster profile %q", fieldRoot, p)}
}

func searchForTestDuplicates(tests []api.TestStepConfiguration) []error {
	duplicates := make(map[string]bool, len(tests))
	var testNames []string

	for _, test := range tests {
		if _, exist := duplicates[test.As]; exist {
			testNames = append(testNames, test.As)
		} else {
			duplicates[test.As] = true
		}
	}

	if testNames != nil {
		return []error{fmt.Errorf("tests: found duplicated test: (%s)", strings.Join(testNames, ","))}
	}
	return nil
}

func validateTestConfigurationType(fieldRoot string, test api.TestStepConfiguration, release *api.ReleaseTagConfiguration, releases sets.String, resolved bool) []error {
	var validationErrors []error
	typeCount := 0
	if testConfig := test.ContainerTestConfiguration; testConfig != nil {
		typeCount++
		if testConfig.MemoryBackedVolume != nil {
			if _, err := resource.ParseQuantity(testConfig.MemoryBackedVolume.Size); err != nil {
				validationErrors = append(validationErrors, fmt.Errorf("%s.memory_backed_volume: 'size' must be a Kubernetes quantity: %w", fieldRoot, err))
			}
		}
		if len(testConfig.From) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s: 'from' is required", fieldRoot))
		}
	}
	var needsReleaseRpms bool
	if testConfig := test.OpenshiftAnsibleClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftAnsibleSrcClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftAnsibleCustomClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerUPIClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerUPISrcClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerCustomTestImageClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.MultiStageTestConfiguration; testConfig != nil {
		if resolved {
			validationErrors = append(validationErrors, fmt.Errorf("%s: non-literal test found in fully-resolved configuration", fieldRoot))
		}
		typeCount++
		if testConfig.ClusterProfile != "" {
			validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
		}
		context := newContext(fieldRoot, testConfig.Environment, releases)
		validationErrors = append(validationErrors, validateLeases(context.forField(".leases"), testConfig.Leases)...)
		validationErrors = append(validationErrors, validateTestSteps(context.forField(".pre"), testStagePre, testConfig.Pre)...)
		validationErrors = append(validationErrors, validateTestSteps(context.forField(".test"), testStageTest, testConfig.Test)...)
		validationErrors = append(validationErrors, validateTestSteps(context.forField(".post"), testStagePost, testConfig.Post)...)
	}
	if testConfig := test.MultiStageTestConfigurationLiteral; testConfig != nil {
		typeCount++
		context := newContext(fieldRoot, testConfig.Environment, releases)
		if testConfig.ClusterProfile != "" {
			validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
		}
		validationErrors = append(validationErrors, validateLeases(context.forField(".leases"), testConfig.Leases)...)
		for i, s := range testConfig.Pre {
			validationErrors = append(validationErrors, validateLiteralTestStep(context.forField(fmt.Sprintf(".pre[%d]", i)), testStagePre, s)...)
		}
		for i, s := range testConfig.Test {
			validationErrors = append(validationErrors, validateLiteralTestStep(context.forField(fmt.Sprintf(".test[%d]", i)), testStageTest, s)...)
		}
		for i, s := range testConfig.Post {
			validationErrors = append(validationErrors, validateLiteralTestStep(context.forField(fmt.Sprintf(".post[%d]", i)), testStagePost, s)...)
		}
	}
	if typeCount == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s has no type, you may want to specify 'container' for a container based test", fieldRoot))
	} else if typeCount == 1 {
		if needsReleaseRpms && release == nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s requires a release in 'tag_specification'", fieldRoot))
		}
	} else if typeCount > 1 {
		validationErrors = append(validationErrors, fmt.Errorf("%s has more than one type", fieldRoot))
	}

	return validationErrors
}

func validateTestSteps(context context, stage testStage, steps []api.TestStep) (ret []error) {
	for i, s := range steps {
		contextI := context.forField(fmt.Sprintf("[%d]", i))
		ret = append(ret, validateTestStep(&contextI, s)...)
		if s.LiteralTestStep != nil {
			ret = append(ret, validateLiteralTestStep(contextI, stage, *s.LiteralTestStep)...)
		}
	}
	return
}

func validateTestStep(context *context, step api.TestStep) (ret []error) {
	if (step.LiteralTestStep != nil && step.Reference != nil) ||
		(step.LiteralTestStep != nil && step.Chain != nil) ||
		(step.Reference != nil && step.Chain != nil) {
		ret = append(ret, fmt.Errorf("%s: only one of `ref`, `chain`, or a literal test step can be set", context.fieldRoot))
		return
	}
	if step.LiteralTestStep == nil && step.Reference == nil && step.Chain == nil {
		ret = append(ret, fmt.Errorf("%s: a reference, chain, or literal test step is required", context.fieldRoot))
		return
	}
	if step.Reference != nil {
		if len(*step.Reference) == 0 {
			ret = append(ret, fmt.Errorf("%s.ref: length cannot be 0", context.fieldRoot))
		} else if context.seen.Has(*step.Reference) {
			ret = append(ret, fmt.Errorf("%s.ref: duplicated name %q", context.fieldRoot, *step.Reference))
		} else {
			context.seen.Insert(*step.Reference)
		}
	}
	if step.Chain != nil {
		if len(*step.Chain) == 0 {
			ret = append(ret, fmt.Errorf("%s.chain: length cannot be 0", context.fieldRoot))
		} else if context.seen.Has(*step.Chain) {
			ret = append(ret, fmt.Errorf("%s.chain: duplicated name %q", context.fieldRoot, *step.Chain))
		} else {
			context.seen.Insert(*step.Chain)
		}
	}
	return
}

func validateLiteralTestStep(context context, stage testStage, step api.LiteralTestStep) (ret []error) {
	if len(step.As) == 0 {
		ret = append(ret, fmt.Errorf("%s: `as` is required", context.fieldRoot))
	} else if context.seen.Has(step.As) {
		ret = append(ret, fmt.Errorf("%s: duplicated name %q", context.fieldRoot, step.As))
	} else {
		context.seen.Insert(step.As)
	}
	if len(step.From) == 0 && step.FromImage == nil {
		ret = append(ret, fmt.Errorf("%s: `from` or `from_image` is required", context.fieldRoot))
	} else if len(step.From) != 0 && step.FromImage != nil {
		ret = append(ret, fmt.Errorf("%s: `from` and `from_image` cannot be set together", context.fieldRoot))
	} else if step.FromImage != nil {
		if step.FromImage.Namespace == "" {
			ret = append(ret, fmt.Errorf("%s.from_image: `namespace` is required", context.fieldRoot))
		}
		if step.FromImage.Name == "" {
			ret = append(ret, fmt.Errorf("%s.from_image: `name` is required", context.fieldRoot))
		}
		if step.FromImage.Tag == "" {
			ret = append(ret, fmt.Errorf("%s.from_image: `tag` is required", context.fieldRoot))
		}
	} else {
		imageParts := strings.Split(step.From, ":")
		if len(imageParts) > 2 {
			ret = append(ret, fmt.Errorf("%s.from: '%s' is not a valid imagestream reference", context.fieldRoot, step.From))
		}
		for i, obj := range imageParts {
			if len(validation.IsDNS1123Subdomain(obj)) != 0 {
				ret = append(ret, fmt.Errorf("%s.from: '%s' is not a valid Kubernetes object name", context.fieldRoot, obj))
			} else if i == 0 && len(imageParts) == 2 {
				switch obj {
				case api.PipelineImageStream, api.ReleaseStreamFor(api.LatestReleaseName), api.ReleaseStreamFor(api.InitialReleaseName), api.ReleaseImageStream:
				default:
					releaseName := api.ReleaseNameFrom(obj)
					if !context.releases.Has(releaseName) {
						ret = append(ret, fmt.Errorf("%s.from: unknown imagestream '%s'", context.fieldRoot, imageParts[0]))
					}

				}
			}
		}
	}
	if len(step.Commands) == 0 {
		ret = append(ret, fmt.Errorf("%s: `commands` is required", context.fieldRoot))
	}
	ret = append(ret, validateResourceRequirements(context.fieldRoot+".resources", step.Resources)...)
	ret = append(ret, validateCredentials(context.fieldRoot, step.Credentials)...)
	if err := validateParameters(&context, step.Environment); err != nil {
		ret = append(ret, err)
	}
	ret = append(ret, validateDependencies(context.fieldRoot, step.Dependencies)...)
	ret = append(ret, validateLeases(context.forField(".leases"), step.Leases)...)
	switch stage {
	case testStagePre, testStageTest:
		if step.OptionalOnSuccess != nil {
			ret = append(ret, fmt.Errorf("%s: `optional_on_success` is only allowed for Post steps", context.fieldRoot))
		}
	}
	return
}

func validateCredentials(fieldRoot string, credentials []api.CredentialReference) []error {
	var errs []error
	for i, credential := range credentials {
		if credential.Name == "" {
			errs = append(errs, fmt.Errorf("%s.credentials[%d].name cannot be empty", fieldRoot, i))
		}
		if credential.Namespace == "" {
			errs = append(errs, fmt.Errorf("%s.credentials[%d].namespace cannot be empty", fieldRoot, i))
		}
		if credential.MountPath == "" {
			errs = append(errs, fmt.Errorf("%s.credentials[%d].mountPath cannot be empty", fieldRoot, i))
		} else if !filepath.IsAbs(credential.MountPath) {
			errs = append(errs, fmt.Errorf("%s.credentials[%d].mountPath is not absolute: %s", fieldRoot, i, credential.MountPath))
		}
		for j, other := range credentials[i+1:] {
			index := i + j + 1
			if credential.MountPath == other.MountPath {
				errs = append(errs, fmt.Errorf("%s.credentials[%d] and credentials[%d] mount to the same location (%s)", fieldRoot, i, index, credential.MountPath))
				continue
			}
			// we can make a couple of assumptions here to improve our check:
			//  - valid mount paths must be absolute paths
			//  - given two absolute paths, a relative path between A and B will
			//    never contain '..' if B is a subdirectory of A
			relPath, err := filepath.Rel(other.MountPath, credential.MountPath)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s.credentials[%d] could not check relative path to credentials[%d] (%w)", fieldRoot, i, index, err))
				continue
			}
			if !strings.Contains(relPath, "..") {
				errs = append(errs, fmt.Errorf("%s.credentials[%d] mounts at %s, which is under credentials[%d] (%s)", fieldRoot, i, credential.MountPath, index, other.MountPath))
			}
			relPath, err = filepath.Rel(credential.MountPath, other.MountPath)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s.credentials[%d] could not check relative path to credentials[%d] (%w)", fieldRoot, index, i, err))
				continue
			}
			if !strings.Contains(relPath, "..") {
				errs = append(errs, fmt.Errorf("%s.credentials[%d] mounts at %s, which is under credentials[%d] (%s)", fieldRoot, index, other.MountPath, i, credential.MountPath))
			}
		}
	}
	return errs
}

func validateParameters(context *context, params []api.StepParameter) error {
	var missing []string
	for _, param := range params {
		if param.Default != nil {
			continue
		}
		if _, ok := context.env[param.Name]; !ok {
			missing = append(missing, param.Name)
		}
	}
	if missing != nil {
		return fmt.Errorf("%s: unresolved parameter(s): %s", context.fieldRoot, missing)
	}
	return nil
}

func validateDependencies(fieldRoot string, dependencies []api.StepDependency) []error {
	var errs []error
	env := sets.NewString()
	for i, dependency := range dependencies {
		if dependency.Name == "" {
			errs = append(errs, fmt.Errorf("%s.dependencies[%d].name must be set", fieldRoot, i))
		} else if numColons := strings.Count(dependency.Name, ":"); !(numColons == 0 || numColons == 1) {
			errs = append(errs, fmt.Errorf("%s.dependencies[%d].name must take the `tag` or `stream:tag` form, not %q", fieldRoot, i, dependency.Name))
		}
		if dependency.Env == "" {
			errs = append(errs, fmt.Errorf("%s.dependencies[%d].env must be set", fieldRoot, i))
		} else if env.Has(dependency.Env) {
			errs = append(errs, fmt.Errorf("%s.dependencies[%d].env targets an environment variable that is already set by another dependency", fieldRoot, i))
		} else {
			env.Insert(dependency.Env)
		}
	}
	return errs
}

func validateLeases(context context, leases []api.StepLease) (ret []error) {
	for i, l := range leases {
		if l.ResourceType == "" {
			ret = append(ret, fmt.Errorf("%s[%d]: 'resource_type' cannot be empty", context.fieldRoot, i))
		}
		if l.Env == "" {
			ret = append(ret, fmt.Errorf("%s[%d]: 'env' cannot be empty", context.fieldRoot, i))
		} else if context.leasesSeen.Has(l.Env) {
			ret = append(ret, fmt.Errorf("%s[%d]: duplicate environment variable: %s", context.fieldRoot, i, l.Env))
		} else {
			context.leasesSeen.Insert(l.Env)
		}
	}
	return
}
