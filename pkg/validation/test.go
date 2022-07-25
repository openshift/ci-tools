package validation

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/robfig/cron.v2"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/openshift/ci-tools/pkg/api"
)

// testStage is the point in a multi-stage test where a step is located.
// `Unknown` is used when validation occurs at a point where that cannot be
// determined (e.g. when validating references, which can be used in different
// contexts).
type testStage uint8

// testInputImages stores tags referenced in `from_image` fields.
// This is different from other pipeline images as several steps across all
// tests can reference the same tag: the validation here mirrors the
// configuration processing code in `pkg/defaults`.  Only the first field that
// references each tag is recorded since reporting all of them would not be
// terribly useful.
type testInputImages map[api.PipelineImageStreamTagReference]fieldPath

const (
	testStageUnknown testStage = iota
	testStagePre
	testStageTest
	testStagePost

	// These are a bit arbitrary but they reflect what was working when I set
	// these limits. Tests with claims must be shorter because they infer
	// more things from the name
	maxClaimTestNameLength = 42
	maxTestNameLength      = 61
)

func (v *Validator) commandHasTrap(cmd string) bool {
	if v.hasTrapCache == nil {
		return trapPattern.MatchString(cmd)
	}
	ret, ok := v.hasTrapCache[cmd]
	if !ok {
		ret = trapPattern.MatchString(cmd)
		v.hasTrapCache[cmd] = ret
	}
	return ret
}

// context contains the information from parent components.
// All but `field` can be nil if the validation being performed in
// context-independent.
type context struct {
	// field is the full path to the current field, used in error messages.
	field fieldPath
	// env is used to validate that all step parameters are set.
	env api.TestEnvironment
	// namesSeen is used to validate that step names are unique.
	namesSeen sets.String
	// leasesSeen is used to validate that lease variable names are unique.
	leasesSeen sets.String
	// inputImagesSeen is used to accumulate input images across tests.
	inputImagesSeen testInputImages
	// releases is used to validate references to release images .
	releases sets.String
}

// newContext creates a top-level context.
func newContext(
	field fieldPath,
	env api.TestEnvironment,
	releases sets.String,
	inputImagesSeen testInputImages,
) *context {
	return &context{
		field:           field,
		env:             env,
		namesSeen:       sets.NewString(),
		leasesSeen:      sets.NewString(),
		inputImagesSeen: inputImagesSeen,
		releases:        releases,
	}
}

func (c context) addField(name string) *context {
	c.field = c.field.addField(name)
	return &c
}

func (c context) addIndex(i int) *context {
	c.field = c.field.addIndex(i)
	return &c
}

func (c *context) errorf(format string, args ...interface{}) error {
	return c.field.errorf(format, args...)
}

var trapPattern = regexp.MustCompile(`(^|\W)\s*trap\s*['"]?\w*['"]?\s*\w*`)

// IsValidReference validates the contents of a registry reference.
// Checks that are context-dependent (whether all parameters are set in a parent
// component, the image references exist in the test configuration, etc.) are
// not performed.
func (v *Validator) IsValidReference(step api.LiteralTestStep) []error {
	return v.validateLiteralTestStep(&context{field: fieldPath(step.As)}, testStageUnknown, step, nil)
}

func (v *Validator) validateTestStepConfiguration(
	configCtx *configContext,
	fieldRoot string,
	input []api.TestStepConfiguration,
	release *api.ReleaseTagConfiguration,
	releases, images sets.String,
	resolved bool,
) []error {
	var validationErrors []error

	// check for test.As duplicates
	validationErrors = append(validationErrors, searchForTestDuplicates(input)...)
	inputImagesSeen := make(testInputImages)
	for num, test := range input {
		fieldRootN := fmt.Sprintf("%s[%d]", fieldRoot, num)
		if len(test.As) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: is required", fieldRootN))
		} else if l := len(test.As); l > maxTestNameLength {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: %d characters long, maximum length is %d", fieldRootN, l, maxTestNameLength))
		} else if l := len(test.As); l > maxClaimTestNameLength && test.ClusterClaim != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: %d characters long, maximum length is %d for tests with claims", fieldRootN, l, maxClaimTestNameLength))
		} else if test.As == "images" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: should not be called 'images' because it gets confused with '[images]' target", fieldRootN))
		} else if strings.HasPrefix(test.As, string(api.PipelineImageStreamTagReferenceIndexImage)) {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: should not begin with 'ci-index' because it gets confused with 'ci-index' and `ci-index-...` targets", fieldRootN))
		} else if images.Has(test.As) {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: duplicated name %q already declared in 'images'", fieldRootN, test.As))
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
		if test.Postsubmit && test.Optional {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `optional` and `postsubmit` are mututally exclusive", fieldRootN))
		}

		if test.Cron != nil && test.Interval != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `interval` and `cron` cannot both be set", fieldRootN))
		}
		if test.Cron != nil && test.ReleaseController {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `cron` cannot be set for release controller jobs", fieldRootN))
		}
		if test.Interval != nil && test.ReleaseController {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `interval` cannot be set for release controller jobs", fieldRootN))
		}
		if (test.Cron != nil || test.Interval != nil) && (test.RunIfChanged != "" || test.SkipIfOnlyChanged != "" || test.Optional) {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `cron` and `interval` are mutually exclusive with `run_if_changed`/`skip_if_only_changed`/`optional`", fieldRootN))
		}
		if test.RunIfChanged != "" && test.SkipIfOnlyChanged != "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `run_if_changed` and `skip_if_only_changed` are mutually exclusive", fieldRootN))
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

		maxJobTimeout := time.Hour * 8
		if test.Timeout != nil && test.Timeout.Duration > maxJobTimeout {
			validationErrors = append(validationErrors, fmt.Errorf("%s: job timeout is limited to %s", fieldRootN, maxJobTimeout))
		}

		// Validate Secret/Secrets
		if test.Secret != nil && test.Secrets != nil {
			validationErrors = append(validationErrors, fmt.Errorf("test.Secret and test.Secrets cannot both be set"))
		}

		if test.Secret != nil {
			test.Secrets = append(test.Secrets, test.Secret)
		}

		if test.Secrets != nil && test.ContainerTestConfiguration == nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: secret/secrets can be only used with container-based tests (use credentials in multi-stage tests)", fieldRootN))
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
					validationErrors = append(validationErrors, fmt.Errorf("%s.path: '%s' secret mount path must be an absolute path", fieldRootN, secret.MountPath))
				}
			}
		}

		validationErrors = append(validationErrors, v.validateTestConfigurationType(fieldRootN, test, release, releases, inputImagesSeen, resolved)...)
	}
	for tag, field := range inputImagesSeen {
		if err := configCtx.AddField(string(field)).addPipelineImage(tag); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	return validationErrors
}

// validateTestStepDependencies ensures that users have referenced valid dependencies
func validateTestStepDependencies(config *api.ReleaseBuildConfiguration) []error {
	hasOverride := func(test *api.TestStepConfiguration, dep string) bool {
		// see if there are any dependency overrides specified for the dependency
		if test.MultiStageTestConfigurationLiteral == nil && test.MultiStageTestConfiguration == nil {
			return false
		}
		if test.MultiStageTestConfigurationLiteral != nil {
			_, ok := test.MultiStageTestConfigurationLiteral.DependencyOverrides[dep]
			return ok
		} else {
			_, ok := test.MultiStageTestConfiguration.DependencyOverrides[dep]
			return ok
		}
	}

	dependencyErrors := func(step api.LiteralTestStep, testIdx int, stageField, stepField string, stepIdx int, claimRelease *api.ClaimRelease) []error {
		var errs []error
		for dependencyIdx, dependency := range step.Dependencies {
			validationError := func(message string) error {
				return fmt.Errorf("tests[%d].%s.%s[%d].dependencies[%d]: cannot determine source for dependency %q - %s", testIdx, stageField, stepField, stepIdx, dependencyIdx, dependency.Name, message)
			}
			stream, name, explicit := config.DependencyParts(dependency, claimRelease)
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
					if claimRelease != nil {
						explicitlyConfigured = explicitlyConfigured || releaseName == claimRelease.ReleaseName
					}
					if !(implicitlyConfigured || explicitlyConfigured) {
						errs = append(errs, validationError(fmt.Sprintf("this dependency requires a %q release, which is not configured", releaseName)))
					}
				}

				test := &config.Tests[testIdx]
				if stream == api.PipelineImageStream {
					switch name {
					case string(api.PipelineImageStreamTagReferenceRoot):
						if config.InputConfiguration.BuildRootImage == nil && !hasOverride(test, dependency.Env) {
							errs = append(errs, validationError("this dependency requires a build root, which is not configured"))
						}
					case string(api.PipelineImageStreamTagReferenceSource):
						// always present
					case string(api.PipelineImageStreamTagReferenceBinaries):
						if config.BinaryBuildCommands == "" && !hasOverride(test, dependency.Env) {
							errs = append(errs, validationError("this dependency requires built binaries, which are not configured"))
						}
					case string(api.PipelineImageStreamTagReferenceTestBinaries):
						if config.TestBinaryBuildCommands == "" && !hasOverride(test, dependency.Env) {
							errs = append(errs, validationError("this dependency requires built test binaries, which are not configured"))
						}
					case string(api.PipelineImageStreamTagReferenceRPMs):
						if config.RpmBuildCommands == "" && !hasOverride(test, dependency.Env) {
							errs = append(errs, validationError("this dependency requires built RPMs, which are not configured"))
						}
					case string(api.PipelineImageStreamTagReferenceIndexImage):
						if config.Operator == nil && !hasOverride(test, dependency.Env) {
							errs = append(errs, validationError("this dependency requires an operator bundle configuration, which is not configured"))
						}
					default:
						// this could be a named index image
						if api.IsIndexImage(name) {
							if config.Operator != nil || len(test.MultiStageTestConfigurationLiteral.DependencyOverrides) > 0 {
								foundBundle := false
								if config.Operator != nil {
									for _, bundle := range config.Operator.Bundles {
										if api.IndexName(bundle.As) == name {
											foundBundle = true
											break
										}
									}
								}
								if !foundBundle {
									// see if there's a dependency override for the index image
									foundBundle = hasOverride(test, dependency.Env)
									if !foundBundle {
										errs = append(errs, validationError(fmt.Sprintf("this dependency requires an operator bundle named %s, which is not configured", strings.TrimPrefix(name, string(api.PipelineImageStreamTagReferenceIndexImage)))))
									}
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
	processSteps := func(steps []api.TestStep, testIdx int, stageField, stepField string, claimRelease *api.ClaimRelease) []error {
		var errs []error
		for stepIdx, test := range steps {
			if test.LiteralTestStep != nil {
				errs = append(errs, dependencyErrors(*test.LiteralTestStep, testIdx, stageField, stepField, stepIdx, claimRelease)...)
			}
		}
		return errs
	}
	processLiteralSteps := func(steps []api.LiteralTestStep, testIdx int, stageField, stepField string, claimRelease *api.ClaimRelease) []error {
		var errs []error
		for stepIdx, test := range steps {
			errs = append(errs, dependencyErrors(test, testIdx, stageField, stepField, stepIdx, claimRelease)...)
		}
		return errs
	}
	var errs []error
	for testIdx, test := range config.Tests {
		var claimRelease *api.ClaimRelease
		if test.ClusterClaim != nil {
			claimRelease = test.ClusterClaim.ClaimRelease(test.As)
		}
		if test.MultiStageTestConfiguration != nil {
			for _, item := range []struct {
				field string
				list  []api.TestStep
			}{
				{field: "pre", list: test.MultiStageTestConfiguration.Pre},
				{field: "test", list: test.MultiStageTestConfiguration.Test},
				{field: "post", list: test.MultiStageTestConfiguration.Post},
			} {
				errs = append(errs, processSteps(item.list, testIdx, "steps", item.field, claimRelease)...)
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
				errs = append(errs, processLiteralSteps(item.list, testIdx, "literal_steps", item.field, claimRelease)...)
			}
		}
	}
	return errs
}

func (v *Validator) validateClusterProfile(fieldRoot string, p api.ClusterProfile) []error {
	if v.validClusterProfiles != nil {
		if _, ok := v.validClusterProfiles[p]; ok {
			return nil
		}
	} else {
		for _, x := range api.ClusterProfiles() {
			if x == p {
				return nil
			}
		}
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

func (v *Validator) validateTestConfigurationType(
	fieldRoot string,
	test api.TestStepConfiguration,
	release *api.ReleaseTagConfiguration,
	releases sets.String,
	inputImagesSeen testInputImages,
	resolved bool,
) []error {
	var validationErrors []error
	clusterCount := 0
	if claim := test.ClusterClaim; claim != nil {
		clusterCount++
		for key := range claim.Labels {
			if key == "product" || key == "version" || key == "architecture" || key == "cloud" || key == "owner" {
				validationErrors = append(validationErrors, fmt.Errorf("%s.cluster_claim.labels contains an invalid key in claim's label: %s", fieldRoot, key))
			}
		}
		if claim.Version == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.cluster_claim.version cannot be empty when cluster_claim is not nil", fieldRoot))
		}
		if claim.Cloud == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.cluster_claim.cloud cannot be empty when cluster_claim is not nil", fieldRoot))
		}
		if claim.Owner == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.cluster_claim.owner cannot be empty when cluster_claim is not nil", fieldRoot))
		}
		if test.MultiStageTestConfigurationLiteral == nil && test.MultiStageTestConfiguration == nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s.cluster_claim cannot be set on a test which is not a multi-stage test", fieldRoot))
		}
	}
	typeCount := 0
	if cluster := test.Cluster; cluster != "" && !api.ValidClusterName(string(cluster)) {
		validationErrors = append(validationErrors, fmt.Errorf("%s.cluster is not a valid cluster: %s", fieldRoot, string(cluster)))
	}
	if testConfig := test.ContainerTestConfiguration; testConfig != nil {
		typeCount++
		if testConfig.MemoryBackedVolume != nil {
			if _, err := resource.ParseQuantity(testConfig.MemoryBackedVolume.Size); err != nil {
				validationErrors = append(validationErrors, fmt.Errorf("%s.memory_backed_volume: 'size' must be a Kubernetes quantity: %w", fieldRoot, err))
			}
		}
		if testConfig.From == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: 'from' is required", fieldRoot))
		}
	}
	var needsReleaseRpms bool
	if testConfig := test.OpenshiftAnsibleClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, v.validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftAnsibleSrcClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, v.validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftAnsibleCustomClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, v.validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, v.validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerUPIClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, v.validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerUPISrcClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, v.validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerCustomTestImageClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, v.validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	var claimRelease *api.ClaimRelease
	if test.ClusterClaim != nil {
		claimRelease = test.ClusterClaim.ClaimRelease(test.As)
	}
	if testConfig := test.MultiStageTestConfiguration; testConfig != nil {
		if resolved {
			validationErrors = append(validationErrors, fmt.Errorf("%s: non-literal test found in fully-resolved configuration", fieldRoot))
		}
		typeCount++
		if testConfig.ClusterProfile != "" {
			clusterCount++
			validationErrors = append(validationErrors, v.validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
		}
		context := newContext(fieldPath(fieldRoot), testConfig.Environment, releases, inputImagesSeen)
		validationErrors = append(validationErrors, validateLeases(context.addField("leases"), testConfig.Leases)...)
		validationErrors = append(validationErrors, v.validateTestSteps(context.addField("pre"), testStagePre, testConfig.Pre, claimRelease)...)
		validationErrors = append(validationErrors, v.validateTestSteps(context.addField("test"), testStageTest, testConfig.Test, claimRelease)...)
		validationErrors = append(validationErrors, v.validateTestSteps(context.addField("post"), testStagePost, testConfig.Post, claimRelease)...)
	}
	if testConfig := test.MultiStageTestConfigurationLiteral; testConfig != nil {
		typeCount++
		context := newContext(fieldPath(fieldRoot).addField("steps"), testConfig.Environment, releases, inputImagesSeen)
		if testConfig.ClusterProfile != "" {
			clusterCount++
			validationErrors = append(validationErrors, v.validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
		}
		validationErrors = append(validationErrors, validateLeases(context.addField("leases"), testConfig.Leases)...)
		for i, s := range testConfig.Pre {
			validationErrors = append(validationErrors, v.validateLiteralTestStep(context.addField("pre").addIndex(i), testStagePre, s, claimRelease)...)
		}
		for i, s := range testConfig.Test {
			validationErrors = append(validationErrors, v.validateLiteralTestStep(context.addField("test").addIndex(i), testStageTest, s, claimRelease)...)
		}
		for i, s := range testConfig.Post {
			validationErrors = append(validationErrors, v.validateLiteralTestStep(context.addField("post").addIndex(i), testStagePost, s, claimRelease)...)
		}
	}
	if typeCount == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s has no type, you may want to specify 'container' for a container based test", fieldRoot))
	} else if typeCount == 1 {
		if needsReleaseRpms && release == nil && !releases.HasAll(api.LatestReleaseName, api.InitialReleaseName) {
			validationErrors = append(validationErrors, fmt.Errorf("%s requires a release in 'tag_specification' or 'releases'", fieldRoot))
		}
	} else if typeCount > 1 {
		validationErrors = append(validationErrors, fmt.Errorf("%s has more than one type", fieldRoot))
	}
	if clusterCount > 1 {
		validationErrors = append(validationErrors, fmt.Errorf("%s installs more than one cluster, probably it defined both cluster_claim and cluster_profile", fieldRoot))
	}

	return validationErrors
}

func (v *Validator) validateTestSteps(context *context, stage testStage, steps []api.TestStep, claimRelease *api.ClaimRelease) (ret []error) {
	for i, s := range steps {
		contextI := context.addIndex(i)
		ret = append(ret, validateTestStep(contextI, s)...)
		if s.LiteralTestStep != nil {
			ret = append(ret, v.validateLiteralTestStep(contextI, stage, *s.LiteralTestStep, claimRelease)...)
		}
	}
	return
}

func validateTestStep(context *context, step api.TestStep) (ret []error) {
	if (step.LiteralTestStep != nil && step.Reference != nil) ||
		(step.LiteralTestStep != nil && step.Chain != nil) ||
		(step.Reference != nil && step.Chain != nil) {
		ret = append(ret, context.errorf("only one of `ref`, `chain`, or a literal test step can be set"))
		return
	}
	if step.LiteralTestStep == nil && step.Reference == nil && step.Chain == nil {
		ret = append(ret, context.errorf("a reference, chain, or literal test step is required"))
		return
	}
	if step.Reference != nil {
		if len(*step.Reference) == 0 {
			ret = append(ret, context.addField("ref").errorf("length cannot be 0"))
		} else if context.namesSeen.Has(*step.Reference) {
			ret = append(ret, context.addField("ref").errorf("duplicated name %q", *step.Reference))
		} else {
			context.namesSeen.Insert(*step.Reference)
		}
	}
	if step.Chain != nil {
		if len(*step.Chain) == 0 {
			ret = append(ret, context.addField("chain").errorf("length cannot be 0"))
		} else if context.namesSeen.Has(*step.Chain) {
			ret = append(ret, context.addField("chain").errorf("duplicated name %q", *step.Chain))
		} else {
			context.namesSeen.Insert(*step.Chain)
		}
	}
	return
}

func (v *Validator) validateLiteralTestStep(context *context, stage testStage, step api.LiteralTestStep, claimRelease *api.ClaimRelease) (ret []error) {
	if len(step.As) == 0 {
		ret = append(ret, context.errorf("`as` is required"))
	} else if context.namesSeen != nil {
		if context.namesSeen.Has(step.As) {
			ret = append(ret, context.errorf("duplicated name %q", step.As))
		} else {
			context.namesSeen.Insert(step.As)
		}
	}
	var fromImageTag *api.PipelineImageStreamTagReference
	if t, ok := step.FromImageTag(); ok {
		fromImageTag = &t
	}
	ret = append(ret, validateFromAndFromImage(context, step.From, step.FromImage, fromImageTag, claimRelease)...)
	if len(step.Commands) == 0 {
		ret = append(ret, context.errorf("`commands` is required"))
	} else {
		ret = append(ret, v.validateCommands(step)...)
	}

	if step.BestEffort != nil && *step.BestEffort && step.Timeout == nil {
		ret = append(ret, fmt.Errorf("test %s contains best_effort without timeout", step.As))
	}

	ret = append(ret, validateResourceRequirements(string(context.field)+".resources", step.Resources)...)
	ret = append(ret, validateCredentials(string(context.field), step.Credentials)...)
	if context.env != nil {
		if err := validateParameters(context, step.Environment); err != nil {
			ret = append(ret, err)
		}
	}
	ret = append(ret, validateDependencies(string(context.field), step.Dependencies)...)
	ret = append(ret, validateLeases(context.addField("leases"), step.Leases)...)
	switch stage {
	case testStagePre, testStageTest:
		if step.OptionalOnSuccess != nil {
			ret = append(ret, context.errorf("`optional_on_success` is only allowed for Post steps"))
		}
	}
	return ret
}

func validateFromAndFromImage(
	context *context,
	from string,
	fromImage *api.ImageStreamTagReference,
	fromImageTag *api.PipelineImageStreamTagReference,
	claimRelease *api.ClaimRelease,
) []error {
	var ret []error
	if len(from) == 0 && fromImage == nil {
		ret = append(ret, context.errorf("`from` or `from_image` is required"))
	} else if len(from) != 0 && fromImage != nil {
		ret = append(ret, context.errorf("`from` and `from_image` cannot be set together"))
	} else if fromImage != nil {
		imgCtx := context.addField("from_image")
		if fromImage.Namespace == "" {
			ret = append(ret, imgCtx.errorf("`namespace` is required"))
		}
		if fromImage.Name == "" {
			ret = append(ret, imgCtx.errorf("`name` is required"))
		}
		if fromImage.Tag == "" {
			ret = append(ret, imgCtx.errorf("`tag` is required"))
		}
		if context.inputImagesSeen != nil && fromImageTag != nil {
			if _, ok := context.inputImagesSeen[*fromImageTag]; !ok {
				context.inputImagesSeen[*fromImageTag] = imgCtx.field
			}
		}
	} else {
		imageParts := strings.Split(from, ":")
		if len(imageParts) > 2 {
			ret = append(ret, context.addField("from").errorf("'%s' is not a valid imagestream reference", from))
		}
		for i, obj := range imageParts {
			if len(validation.IsDNS1123Subdomain(obj)) != 0 {
				ret = append(ret, context.addField("from").errorf("'%s' is not a valid Kubernetes object name", obj))
			} else if i == 0 && len(imageParts) == 2 {
				switch obj {
				case api.PipelineImageStream, api.ReleaseStreamFor(api.LatestReleaseName), api.ReleaseStreamFor(api.InitialReleaseName), api.ReleaseImageStream:
				default:
					releaseName := api.ReleaseNameFrom(obj)
					if !context.releases.Has(releaseName) && (claimRelease == nil || releaseName != claimRelease.OverrideName) {
						ret = append(ret, context.addField("from").errorf("unknown imagestream '%s'", imageParts[0]))
					}
				}
			}
		}
	}
	return ret
}

func (v *Validator) validateCommands(test api.LiteralTestStep) []error {
	var validationErrors []error
	if v.commandHasTrap(test.Commands) && test.GracePeriod == nil {
		validationErrors = append(validationErrors, fmt.Errorf("test `%s` has `commands` containing `trap` command, but test step is missing grace_period", test.As))
	}

	return validationErrors
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

func ValidateSecretInStep(ns, name string) error {
	// only secrets in test-credentials namespace can be used in a step
	if ns != "test-credentials" {
		return nil
	}
	volumeName := strings.ReplaceAll(fmt.Sprintf("%s-%s", ns, name), ".", "-")
	if valueErrs := validation.IsDNS1123Label(volumeName); len(valueErrs) > 0 {
		return fmt.Errorf("volumeName %s: %v", volumeName, valueErrs)
	}
	return nil
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
		return context.errorf("unresolved parameter(s): %s", missing)
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

func validateDNSConfig(fieldRoot string, dnsConfig []api.StepDNSConfig) (ret []error) {
	var errs []error
	for i, dnsconfig := range dnsConfig {
		if dnsconfig.Searches[i] == "" {
			errs = append(errs, fmt.Errorf("%s.searches[%d] must be set", fieldRoot, i))
		}
	}

	return errs
}

func validateLeases(context *context, leases []api.StepLease) (ret []error) {
	for i, l := range leases {
		if l.ResourceType == "" {
			ret = append(ret, context.addIndex(i).errorf("'resource_type' cannot be empty"))
		}
		if l.Env == "" {
			ret = append(ret, context.addIndex(i).errorf("'env' cannot be empty"))
		} else if context.leasesSeen != nil {
			if context.leasesSeen.Has(l.Env) {
				ret = append(ret, context.addIndex(i).errorf("duplicate environment variable: %s", l.Env))
			} else {
				context.leasesSeen.Insert(l.Env)
			}
		}
	}
	return
}
