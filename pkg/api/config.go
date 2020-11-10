package api

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
)

const defaultArtifacts = "/tmp/artifacts"

// Default sets default values after loading but before validation
func (config *ReleaseBuildConfiguration) Default() {
	def := func(p *string) {
		if *p == "" {
			*p = defaultArtifacts
		}
	}
	defTest := func(t *TestStepConfiguration) {
		def(&t.ArtifactDir)
		if s := t.MultiStageTestConfigurationLiteral; s != nil {
			for i := range s.Pre {
				def(&s.Pre[i].ArtifactDir)
			}
			for i := range s.Test {
				def(&s.Test[i].ArtifactDir)
			}
			for i := range s.Post {
				def(&s.Post[i].ArtifactDir)
			}
		}
	}
	for _, step := range config.RawSteps {
		if test := step.TestStepConfiguration; test != nil {
			defTest(test)
		}
	}
	for _, test := range config.Tests {
		defTest(&test)
	}
}

// ValidateAtRuntime validates all the configuration's values without knowledge of config
// repo structure
func (config *ReleaseBuildConfiguration) ValidateAtRuntime() error {
	return config.validate("", "", false)
}

// ValidateResolved behaves as ValidateAtRuntime and also validates that all
// test steps are fully resolved.
func (config *ReleaseBuildConfiguration) ValidateResolved() error {
	config.Default()
	return config.validate("", "", true)
}

// Validate validates all the configuration's values.
func (config *ReleaseBuildConfiguration) Validate(org, repo string) error {
	config.Default()
	return config.validate(org, repo, false)
}

func (config *ReleaseBuildConfiguration) validate(org, repo string, resolved bool) error {
	var validationErrors []error

	validationErrors = append(validationErrors, validateReleaseBuildConfiguration(config, org, repo)...)
	validationErrors = append(validationErrors, validateBuildRootImageConfiguration("build_root", config.InputConfiguration.BuildRootImage, len(config.Images) > 0))
	validationErrors = append(validationErrors, validateTestStepConfiguration("tests", config.Tests, config.ReleaseTagConfiguration, resolved)...)

	// this validation brings together a large amount of data from separate
	// parts of the configuration, so it's written as a standalone method
	validationErrors = append(validationErrors, config.validateTestStepDependencies()...)

	if config.Images != nil {
		validationErrors = append(validationErrors, validateImages("images", config.Images)...)
	}

	if config.Operator != nil {
		// validateOperator needs a method that maps `substitute.with` values to image links
		// to validate the value is meaningful in the context of the configuration
		linkForImage := func(image string) StepLink {
			imageStream, name, _ := config.DependencyParts(StepDependency{Name: image})
			return LinkForImage(imageStream, name)
		}
		validationErrors = append(validationErrors, validateOperator("operator", config.Operator, linkForImage)...)
	}

	if config.InputConfiguration.BaseImages != nil {
		validationErrors = append(validationErrors, validateImageStreamTagReferenceMap("base_images", config.InputConfiguration.BaseImages)...)
	}

	if config.InputConfiguration.BaseRPMImages != nil {
		validationErrors = append(validationErrors, validateImageStreamTagReferenceMap("base_rpm_images", config.InputConfiguration.BaseRPMImages)...)
	}

	// Validate tag_specification
	if config.InputConfiguration.ReleaseTagConfiguration != nil {
		validationErrors = append(validationErrors, validateReleaseTagConfiguration("tag_specification", *config.InputConfiguration.ReleaseTagConfiguration)...)
	}

	// Validate promotion
	if config.PromotionConfiguration != nil {
		validationErrors = append(validationErrors, validatePromotionConfiguration("promotion", *config.PromotionConfiguration)...)
	}

	validationErrors = append(validationErrors, validateReleases("releases", config.Releases, config.ReleaseTagConfiguration != nil)...)

	var lines []string
	for _, err := range validationErrors {
		if err == nil {
			continue
		}
		lines = append(lines, err.Error())
	}
	switch len(lines) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("invalid configuration: %s", lines[0])
	default:
		return fmt.Errorf("configuration has %d errors:\n\n  * %s\n", len(lines), strings.Join(lines, "\n  * "))
	}
}

// validateTestStepDependencies ensures that users have referenced valid dependencies
func (config *ReleaseBuildConfiguration) validateTestStepDependencies() []error {
	dependencyErrors := func(step LiteralTestStep, testIdx int, stageField, stepField string, stepIdx int) []error {
		var errs []error
		for dependencyIdx, dependency := range step.Dependencies {
			validationError := func(message string) error {
				return fmt.Errorf("tests[%d].%s.%s[%d].dependencies[%d]: cannot determine source for dependency %q - %s", testIdx, stageField, stepField, stepIdx, dependencyIdx, dependency.Name, message)
			}
			stream, name, explicit := config.DependencyParts(dependency)
			if link := LinkForImage(stream, name); link == nil {
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
				case IsReleaseStream(stream):
					releaseName = ReleaseNameFrom(stream)
				case IsReleasePayloadStream(stream):
					releaseName = name
				}

				if releaseName != "" {
					implicitlyConfigured := (releaseName == InitialReleaseName || releaseName == LatestReleaseName) && config.InputConfiguration.ReleaseTagConfiguration != nil
					_, explicitlyConfigured := config.InputConfiguration.Releases[releaseName]
					if !(implicitlyConfigured || explicitlyConfigured) {
						errs = append(errs, validationError(fmt.Sprintf("this dependency requires a %q release, which is not configured", releaseName)))
					}
				}

				if stream == PipelineImageStream {
					switch name {
					case string(PipelineImageStreamTagReferenceRoot):
						if config.InputConfiguration.BuildRootImage == nil {
							errs = append(errs, validationError("this dependency requires a build root, which is not configured"))
						}
					case string(PipelineImageStreamTagReferenceSource):
						// always present
					case string(PipelineImageStreamTagReferenceBinaries):
						if config.BinaryBuildCommands == "" {
							errs = append(errs, validationError("this dependency requires built binaries, which are not configured"))
						}
					case string(PipelineImageStreamTagReferenceTestBinaries):
						if config.TestBinaryBuildCommands == "" {
							errs = append(errs, validationError("this dependency requires built test binaries, which are not configured"))
						}
					case string(PipelineImageStreamTagReferenceRPMs):
						if config.RpmBuildCommands == "" {
							errs = append(errs, validationError("this dependency requires built RPMs, which are not configured"))
						}
					case string(PipelineImageStreamTagReferenceIndexImage):
						if config.Operator == nil {
							errs = append(errs, validationError("this dependency requires an operator bundle configuration, which is not configured"))
						}
					default:
						// this could be a base image, or a project image
						if !config.IsBaseImage(name) && !config.BuildsImage(name) {
							errs = append(errs, validationError("no base image import or project image build is configured to provide this dependency"))
						}
					}
				}
			}
		}
		return errs
	}
	processSteps := func(steps []TestStep, testIdx int, stageField, stepField string) []error {
		var errs []error
		for stepIdx, test := range steps {
			if test.LiteralTestStep != nil {
				errs = append(errs, dependencyErrors(*test.LiteralTestStep, testIdx, stageField, stepField, stepIdx)...)
			}
		}
		return errs
	}
	processLiteralSteps := func(steps []LiteralTestStep, testIdx int, stageField, stepField string) []error {
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
				list  []TestStep
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
				list  []LiteralTestStep
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

// ImageStreamFor guesses at the ImageStream that will hold a tag.
// We use this to decipher the user's intent when they provide a
// naked tag in configuration; we support such behavior in order to
// allow users a simpler workflow for the most common cases, like
// referring to `pipeline:src`. If they refer to an ambiguous image,
// however, they will get bad behavior and will need to specify an
// ImageStream as well, for instance release-initial:installer.
// We also return whether the stream is explicit or inferred.
func (config *ReleaseBuildConfiguration) ImageStreamFor(image string) (string, bool) {
	if config.IsPipelineImage(image) || config.BuildsImage(image) {
		return PipelineImageStream, true
	} else {
		return StableImageStream, false
	}
}

// DependencyParts returns the imageStream and tag name from a user-provided
// reference to an image in the test namespace
func (config *ReleaseBuildConfiguration) DependencyParts(dependency StepDependency) (string, string, bool) {
	if !strings.Contains(dependency.Name, ":") {
		stream, explicit := config.ImageStreamFor(dependency.Name)
		return stream, dependency.Name, explicit
	} else {
		parts := strings.Split(dependency.Name, ":")
		return parts[0], parts[1], true
	}
}

func validateBuildRootImageConfiguration(fieldRoot string, input *BuildRootImageConfiguration, hasImages bool) error {
	if input == nil {
		if hasImages {
			return errors.New("when 'images' are specified 'build_root' is required and must have image_stream_tag, project_image or from_repository set")
		}
		return nil
	}

	if input.ProjectImageBuild != nil && input.ImageStreamTagReference != nil {
		return fmt.Errorf("%s: image_stream_tag and project_image are mutually exclusive", fieldRoot)
	}
	if input.ProjectImageBuild != nil && input.FromRepository {
		return fmt.Errorf("%s: project_image and from_repository are mutually exclusive", fieldRoot)
	}
	if input.FromRepository && input.ImageStreamTagReference != nil {
		return fmt.Errorf("%s: from_repository and image_stream_tag are mutually exclusive", fieldRoot)
	}
	if input.ProjectImageBuild == nil && input.ImageStreamTagReference == nil && !input.FromRepository {
		return fmt.Errorf("%s: you have to specify one of project_image, image_stream_tag or from_repository", fieldRoot)
	}
	return nil
}

func validateImages(fieldRoot string, input []ProjectDirectoryImageBuildStepConfiguration) []error {
	var validationErrors []error
	seenNames := map[PipelineImageStreamTagReference]int{}
	for num, image := range input {
		fieldRootN := fmt.Sprintf("%s[%d]", fieldRoot, num)
		if image.To == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `to` must be set", fieldRootN))
		}
		if idx, seen := seenNames[image.To]; seen {
			fieldRootIdx := fmt.Sprintf("%s[%d]", fieldRoot, idx)
			validationErrors = append(validationErrors, fmt.Errorf("%s: duplicate image name '%s' (previously seen in %s)", fieldRootN, string(image.To), fieldRootIdx))
		}
		seenNames[image.To] = num
		if image.To == PipelineImageStreamTagReferenceBundleSource {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `to` cannot be %s", fieldRootN, PipelineImageStreamTagReferenceBundleSource))
		}
		if IsBundleImage(string(image.To)) {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `to` cannot begin with `%s`", fieldRootN, bundlePrefix))
		}
		if image.To == PipelineImageStreamTagReferenceIndexImageGenerator {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `to` cannot be %s", fieldRootN, PipelineImageStreamTagReferenceIndexImageGenerator))
		}
		if image.To == PipelineImageStreamTagReferenceIndexImage {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `to` cannot be %s", fieldRootN, PipelineImageStreamTagReferenceIndexImage))
		}
	}
	return validationErrors
}

func validateOperator(fieldRoot string, input *OperatorStepConfiguration, linkForImage func(string) StepLink) []error {
	var validationErrors []error
	for num, sub := range input.Substitutions {
		fieldRootN := fmt.Sprintf("%s.substitute[%d]", fieldRoot, num)
		if sub.PullSpec == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.pullspec: must be set", fieldRootN))
		}
		if sub.With == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.with: must be set", fieldRootN))
		}

		if link := linkForImage(sub.With); link == nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s.with: could not resolve '%s' to an image involved in the config", fieldRootN, sub.With))
		}
	}
	return validationErrors
}

func validateTestStepConfiguration(fieldRoot string, input []TestStepConfiguration, release *ReleaseTagConfiguration, resolved bool) []error {
	var validationErrors []error

	// check for test.As duplicates
	validationErrors = append(validationErrors, searchForTestDuplicates(input)...)

	for num, test := range input {
		fieldRootN := fmt.Sprintf("%s[%d]", fieldRoot, num)
		if len(test.As) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: is required", fieldRootN))
		} else if test.As == "images" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: should not be called 'images' because it gets confused with '[images]' target", fieldRootN))
		} else if test.As == "ci-index" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: should not be called 'ci-index' because it gets confused with 'ci-index' target", fieldRootN))
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

		validationErrors = append(validationErrors, validateTestConfigurationType(fieldRootN, test, release, resolved)...)
	}
	return validationErrors
}

func validateImageStreamTagReference(fieldRoot string, input ImageStreamTagReference) []error {
	var validationErrors []error

	if len(input.Tag) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.tag: value required but not provided", fieldRoot))
	}

	return validationErrors
}

func validateImageStreamTagReferenceMap(fieldRoot string, input map[string]ImageStreamTagReference) []error {
	var validationErrors []error
	for k, v := range input {
		if k == "root" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s can't be named 'root'", fieldRoot, k))
		}
		validationErrors = append(validationErrors, validateImageStreamTagReference(fmt.Sprintf("%s.%s", fieldRoot, k), v)...)
	}
	return validationErrors
}

func validatePromotionConfiguration(fieldRoot string, input PromotionConfiguration) []error {
	var validationErrors []error

	if len(input.Namespace) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no namespace defined", fieldRoot))
	}

	if len(input.Name) == 0 && len(input.Tag) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no name or tag defined", fieldRoot))
	}

	if len(input.Name) != 0 && len(input.Tag) != 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: both name and tag defined", fieldRoot))
	}
	return validationErrors
}

func validateReleaseTagConfiguration(fieldRoot string, input ReleaseTagConfiguration) []error {
	var validationErrors []error

	if len(input.Namespace) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no namespace defined", fieldRoot))
	}

	if len(input.Name) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no name defined", fieldRoot))
	}

	return validationErrors
}

func validateClusterProfile(fieldRoot string, p ClusterProfile) []error {
	switch p {
	case ClusterProfileAWS, ClusterProfileAWSAtomic, ClusterProfileAWSCentos, ClusterProfileAWSCentos40, ClusterProfileAWSGluster, ClusterProfileAzure4, ClusterProfileGCP, ClusterProfileGCP40, ClusterProfileGCPHA, ClusterProfileGCPCRIO, ClusterProfileGCPLogging, ClusterProfileGCPLoggingJournald, ClusterProfileGCPLoggingJSONFile, ClusterProfileGCPLoggingCRIO, ClusterProfileLibvirtPpc64le, ClusterProfileLibvirtS390x, ClusterProfileOpenStack, ClusterProfileOpenStackOsuosl, ClusterProfileOpenStackVexxhost, ClusterProfileOpenStackPpc64le, ClusterProfileOvirt, ClusterProfilePacket, ClusterProfileVSphere:
		return nil
	}
	return []error{fmt.Errorf("%s: invalid cluster profile %q", fieldRoot, p)}
}

func searchForTestDuplicates(tests []TestStepConfiguration) []error {
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

func validateTestConfigurationType(fieldRoot string, test TestStepConfiguration, release *ReleaseTagConfiguration, resolved bool) []error {
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
	if testConfig := test.OpenshiftAnsible40ClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerSrcClusterTestConfiguration; testConfig != nil {
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
		seen := sets.NewString()
		validationErrors = append(validationErrors, validateTestStepsPre(fmt.Sprintf("%s.Pre", fieldRoot), testConfig.Pre, seen, testConfig.Environment)...)
		validationErrors = append(validationErrors, validateTestStepsTest(fmt.Sprintf("%s.Test", fieldRoot), testConfig.Test, seen, testConfig.Environment)...)
		validationErrors = append(validationErrors, validateTestStepsPost(fmt.Sprintf("%s.Post", fieldRoot), testConfig.Post, seen, testConfig.Environment)...)
	}
	if testConfig := test.MultiStageTestConfigurationLiteral; testConfig != nil {
		typeCount++
		if testConfig.ClusterProfile != "" {
			validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
		}
		seen := sets.NewString()
		for i, s := range testConfig.Pre {
			fieldRootI := fmt.Sprintf("%s.Pre[%d]", fieldRoot, i)
			validationErrors = append(validationErrors, validateLiteralTestStepPre(fieldRootI, s, seen, testConfig.Environment)...)
		}
		for i, s := range testConfig.Test {
			fieldRootI := fmt.Sprintf("%s.Test[%d]", fieldRoot, i)
			validationErrors = append(validationErrors, validateLiteralTestStepTest(fieldRootI, s, seen, testConfig.Environment)...)
		}
		for i, s := range testConfig.Post {
			fieldRootI := fmt.Sprintf("%s.Post[%d]", fieldRoot, i)
			validationErrors = append(validationErrors, validateLiteralTestStepPost(fieldRootI, s, seen, testConfig.Environment)...)
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

func validateTestStepsPre(fieldRoot string, steps []TestStep, seen sets.String, env TestEnvironment) (ret []error) {
	for i, s := range steps {
		fieldRootI := fmt.Sprintf("%s[%d]", fieldRoot, i)
		ret = validateTestStep(fieldRootI, s, seen)
		if s.LiteralTestStep != nil {
			ret = append(ret, validateLiteralTestStepPre(fieldRootI, *s.LiteralTestStep, seen, env)...)
		}
	}
	return
}

func validateTestStepsTest(fieldRoot string, steps []TestStep, seen sets.String, env TestEnvironment) (ret []error) {
	for i, s := range steps {
		fieldRootI := fmt.Sprintf("%s[%d]", fieldRoot, i)
		ret = validateTestStep(fieldRootI, s, seen)
		if s.LiteralTestStep != nil {
			ret = append(ret, validateLiteralTestStepTest(fieldRootI, *s.LiteralTestStep, seen, env)...)
		}
	}
	return
}

func validateTestStepsPost(fieldRoot string, steps []TestStep, seen sets.String, env TestEnvironment) (ret []error) {
	for i, s := range steps {
		fieldRootI := fmt.Sprintf("%s[%d]", fieldRoot, i)
		ret = validateTestStep(fieldRootI, s, seen)
		if s.LiteralTestStep != nil {
			ret = append(ret, validateLiteralTestStepPost(fieldRootI, *s.LiteralTestStep, seen, env)...)
		}
	}
	return
}

func validateTestStep(fieldRootI string, step TestStep, seen sets.String) (ret []error) {
	if (step.LiteralTestStep != nil && step.Reference != nil) ||
		(step.LiteralTestStep != nil && step.Chain != nil) ||
		(step.Reference != nil && step.Chain != nil) {
		ret = append(ret, fmt.Errorf("%s: only one of `ref`, `chain`, or a literal test step can be set", fieldRootI))
		return
	}
	if step.LiteralTestStep == nil && step.Reference == nil && step.Chain == nil {
		ret = append(ret, fmt.Errorf("%s: a reference, chain, or literal test step is required", fieldRootI))
		return
	}
	if step.Reference != nil {
		if len(*step.Reference) == 0 {
			ret = append(ret, fmt.Errorf("%s.ref: length cannot be 0", fieldRootI))
		} else if seen.Has(*step.Reference) {
			ret = append(ret, fmt.Errorf("%s.ref: duplicated name %q", fieldRootI, *step.Reference))
		} else {
			seen.Insert(*step.Reference)
		}
	}
	if step.Chain != nil {
		if len(*step.Chain) == 0 {
			ret = append(ret, fmt.Errorf("%s.chain: length cannot be 0", fieldRootI))
		} else if seen.Has(*step.Chain) {
			ret = append(ret, fmt.Errorf("%s.chain: duplicated name %q", fieldRootI, *step.Chain))
		} else {
			seen.Insert(*step.Chain)
		}
	}
	return
}

func validateLiteralTestStepCommon(fieldRoot string, step LiteralTestStep, seen sets.String, env TestEnvironment) (ret []error) {
	if len(step.As) == 0 {
		ret = append(ret, fmt.Errorf("%s: `as` is required", fieldRoot))
	} else if seen.Has(step.As) {
		ret = append(ret, fmt.Errorf("%s: duplicated name %q", fieldRoot, step.As))
	} else {
		seen.Insert(step.As)
	}
	if len(step.From) == 0 && step.FromImage == nil {
		ret = append(ret, fmt.Errorf("%s: `from` or `from_image` is required", fieldRoot))
	} else if len(step.From) != 0 && step.FromImage != nil {
		ret = append(ret, fmt.Errorf("%s: `from` and `from_image` cannot be set together", fieldRoot))
	} else if step.FromImage != nil {
		if step.FromImage.Namespace == "" {
			ret = append(ret, fmt.Errorf("%s.from_image: `namespace` is required", fieldRoot))
		}
		if step.FromImage.Name == "" {
			ret = append(ret, fmt.Errorf("%s.from_image: `name` is required", fieldRoot))
		}
		if step.FromImage.Tag == "" {
			ret = append(ret, fmt.Errorf("%s.from_image: `tag` is required", fieldRoot))
		}
	} else {
		imageParts := strings.Split(step.From, ":")
		if len(imageParts) > 2 {
			ret = append(ret, fmt.Errorf("%s.from: '%s' is not a valid imagestream reference", fieldRoot, step.From))
		}
		for i, obj := range imageParts {
			if len(validation.IsDNS1123Subdomain(obj)) != 0 {
				ret = append(ret, fmt.Errorf("%s.from: '%s' is not a valid Kubernetes object name", fieldRoot, obj))
			} else if i == 0 && len(imageParts) == 2 {
				switch obj {
				case PipelineImageStream, ReleaseStreamFor(LatestReleaseName), ReleaseStreamFor(InitialReleaseName), ReleaseImageStream:
				default:
					ret = append(ret, fmt.Errorf("%s.from: unknown imagestream '%s'", fieldRoot, imageParts[0]))
				}
			}
		}
	}
	if len(step.Commands) == 0 {
		ret = append(ret, fmt.Errorf("%s: `commands` is required", fieldRoot))
	}
	ret = append(ret, validateResourceRequirements(fieldRoot+".resources", step.Resources)...)
	ret = append(ret, validateCredentials(fieldRoot, step.Credentials)...)
	if err := validateParameters(fieldRoot, step.Environment, env); err != nil {
		ret = append(ret, err)
	}
	ret = append(ret, validateDependencies(fieldRoot, step.Dependencies)...)
	return
}

func validateLiteralTestStepPre(fieldRoot string, step LiteralTestStep, seen sets.String, env TestEnvironment) (ret []error) {
	ret = validateLiteralTestStepCommon(fieldRoot, step, seen, env)
	if step.OptionalOnSuccess != nil {
		ret = append(ret, fmt.Errorf("%s: `optional_on_success` is only allowed for Post steps", fieldRoot))
	}
	return
}

func validateLiteralTestStepTest(fieldRoot string, step LiteralTestStep, seen sets.String, env TestEnvironment) (ret []error) {
	ret = validateLiteralTestStepPre(fieldRoot, step, seen, env)
	return
}

func validateLiteralTestStepPost(fieldRoot string, step LiteralTestStep, seen sets.String, env TestEnvironment) (ret []error) {
	ret = validateLiteralTestStepCommon(fieldRoot, step, seen, env)
	return
}

func validateCredentials(fieldRoot string, credentials []CredentialReference) []error {
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

func validateParameters(fieldRoot string, params []StepParameter, env TestEnvironment) error {
	var missing []string
	for _, param := range params {
		if param.Default != nil {
			continue
		}
		if _, ok := env[param.Name]; !ok {
			missing = append(missing, param.Name)
		}
	}
	if missing != nil {
		return fmt.Errorf("%s: unresolved parameter(s): %s", fieldRoot, missing)
	}
	return nil
}

func validateDependencies(fieldRoot string, dependencies []StepDependency) []error {
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

func validateReleaseBuildConfiguration(input *ReleaseBuildConfiguration, org, repo string) []error {
	var validationErrors []error

	if len(input.Tests) == 0 && len(input.Images) == 0 {
		validationErrors = append(validationErrors, errors.New("you must define at least one test or image build in 'tests' or 'images'"))
	}

	if len(input.RpmBuildLocation) != 0 && len(input.RpmBuildCommands) == 0 {
		validationErrors = append(validationErrors, errors.New("'rpm_build_location' defined but no 'rpm_build_commands' found"))
	}

	if input.BaseRPMImages != nil && len(input.RpmBuildCommands) == 0 {
		validationErrors = append(validationErrors, errors.New("'base_rpm_images' defined but no 'rpm_build_commands' found"))
	}

	if org != "" && repo != "" {
		if input.CanonicalGoRepository != nil && *input.CanonicalGoRepository == fmt.Sprintf("github.com/%s/%s", org, repo) {
			validationErrors = append(validationErrors, errors.New("'canonical_go_repository' provides the default location, so is unnecessary"))
		}
	}

	validationErrors = append(validationErrors, validateResources("resources", input.Resources)...)
	return validationErrors
}

func validateResources(fieldRoot string, resources ResourceConfiguration) []error {
	var validationErrors []error
	if len(resources) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("'%s' should be specified to provide resource requests", fieldRoot))
	} else {
		if _, exists := resources["*"]; !exists {
			validationErrors = append(validationErrors, fmt.Errorf("'%s' must specify a blanket policy for '*'", fieldRoot))
		}
		for key := range resources {
			validationErrors = append(validationErrors, validateResourceRequirements(fmt.Sprintf("%s.%s", fieldRoot, key), resources[key])...)
		}
	}

	return validationErrors
}

func validateResourceRequirements(fieldRoot string, requirements ResourceRequirements) []error {
	var validationErrors []error

	validationErrors = append(validationErrors, validateResourceList(fmt.Sprintf("%s.limits", fieldRoot), requirements.Limits)...)
	validationErrors = append(validationErrors, validateResourceList(fmt.Sprintf("%s.requests", fieldRoot), requirements.Requests)...)

	if len(requirements.Requests) == 0 && len(requirements.Limits) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("'%s' should have at least one request or limit", fieldRoot))
	}

	return validationErrors
}

func validateResourceList(fieldRoot string, list ResourceList) []error {
	var validationErrors []error

	var numInvalid int
	for key := range list {
		switch key {
		case "cpu", "memory":
			if quantity, err := resource.ParseQuantity(list[key]); err != nil {
				validationErrors = append(validationErrors, fmt.Errorf("%s.%s: invalid quantity: %w", fieldRoot, key, err))
			} else {
				if quantity.IsZero() {
					validationErrors = append(validationErrors, fmt.Errorf("%s.%s: quantity cannot be zero", fieldRoot, key))
				}
				if quantity.Sign() == -1 {
					validationErrors = append(validationErrors, fmt.Errorf("%s.%s: quantity cannot be negative", fieldRoot, key))
				}
			}
		default:
			numInvalid++
			validationErrors = append(validationErrors, fmt.Errorf("'%s' specifies an invalid key %s", fieldRoot, key))
		}
	}

	return validationErrors
}

func validateReleases(fieldRoot string, releases map[string]UnresolvedRelease, hasTagSpec bool) []error {
	var validationErrors []error
	// we need a deterministic iteration for testing
	names := sets.NewString()
	for name := range releases {
		names.Insert(name)
	}
	for _, name := range names.List() {
		release := releases[name]
		if hasTagSpec && name == "latest" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s: cannot request resolving a latest release and set tag_specification", fieldRoot, name))
		}
		var set int
		if release.Candidate != nil {
			set = set + 1
		}
		if release.Release != nil {
			set = set + 1
		}
		if release.Prerelease != nil {
			set = set + 1
		}

		if set > 1 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s: cannot set more than one of candidate, prerelease and release", fieldRoot, name))
		} else if set == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s: must set candidate, prerelease or release", fieldRoot, name))
		} else if release.Candidate != nil {
			validationErrors = append(validationErrors, validateCandidate(fmt.Sprintf("%s.%s", fieldRoot, name), *release.Candidate)...)
		} else if release.Release != nil {
			validationErrors = append(validationErrors, validateRelease(fmt.Sprintf("%s.%s", fieldRoot, name), *release.Release)...)
		} else if release.Prerelease != nil {
			validationErrors = append(validationErrors, validatePrerelease(fmt.Sprintf("%s.%s", fieldRoot, name), *release.Prerelease)...)
		}
	}
	return validationErrors
}

var minorVersionMatcher = regexp.MustCompile(`[0-9]\.[0-9]+`)

func validateCandidate(fieldRoot string, candidate Candidate) []error {
	var validationErrors []error
	if err := validateProduct(fmt.Sprintf("%s.product", fieldRoot), candidate.Product); err != nil {
		validationErrors = append(validationErrors, err)
		return validationErrors
	}

	// we allow an unset architecture, we will default it later
	if candidate.Architecture != "" {
		if err := validateArchitecture(fmt.Sprintf("%s.architecture", fieldRoot), candidate.Architecture); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	streamsByProduct := map[ReleaseProduct]sets.String{
		ReleaseProductOKD: sets.NewString("", string(ReleaseStreamOKD)), // we allow unset and will default it
		ReleaseProductOCP: sets.NewString(string(ReleaseStreamCI), string(ReleaseStreamNightly)),
	}
	if !streamsByProduct[candidate.Product].Has(string(candidate.Stream)) {
		validationErrors = append(validationErrors, fmt.Errorf("%s.stream: must be one of %s", fieldRoot, strings.Join(streamsByProduct[candidate.Product].List(), ", ")))
	}

	if err := validateVersion(fmt.Sprintf("%s.version", fieldRoot), candidate.Version); err != nil {
		validationErrors = append(validationErrors, err)
	}

	if candidate.Relative < 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.relative: must be a positive integer", fieldRoot))
	}

	return validationErrors
}

func validateProduct(fieldRoot string, product ReleaseProduct) error {
	products := sets.NewString(string(ReleaseProductOKD), string(ReleaseProductOCP))
	if !products.Has(string(product)) {
		return fmt.Errorf("%s: must be one of %s", fieldRoot, strings.Join(products.List(), ", "))
	}
	return nil
}

func validateArchitecture(fieldRoot string, architecture ReleaseArchitecture) error {
	architectures := sets.NewString(string(ReleaseArchitectureAMD64), string(ReleaseArchitecturePPC64le), string(ReleaseArchitectureS390x))
	if !architectures.Has(string(architecture)) {
		return fmt.Errorf("%s: must be one of %s", fieldRoot, strings.Join(architectures.List(), ", "))
	}
	return nil
}

func validateVersion(fieldRoot, version string) error {
	if !minorVersionMatcher.MatchString(version) {
		return fmt.Errorf("%s: must be a minor version in the form %s", fieldRoot, minorVersionMatcher.String())
	}
	return nil
}

func validateRelease(fieldRoot string, release Release) []error {
	var validationErrors []error
	// we allow an unset architecture, we will default it later
	if release.Architecture != "" {
		if err := validateArchitecture(fmt.Sprintf("%s.architecture", fieldRoot), release.Architecture); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	channels := sets.NewString(string(ReleaseChannelStable), string(ReleaseChannelFast), string(ReleaseChannelCandidate))
	if !channels.Has(string(release.Channel)) {
		validationErrors = append(validationErrors, fmt.Errorf("%s.channel: must be one of %s", fieldRoot, strings.Join(channels.List(), ", ")))
		return validationErrors
	}

	if err := validateVersion(fmt.Sprintf("%s.version", fieldRoot), release.Version); err != nil {
		validationErrors = append(validationErrors, err)
	}

	return validationErrors
}

func validatePrerelease(fieldRoot string, prerelease Prerelease) []error {
	var validationErrors []error
	if err := validateProduct(fmt.Sprintf("%s.product", fieldRoot), prerelease.Product); err != nil {
		validationErrors = append(validationErrors, err)
		return validationErrors
	}

	// we allow an unset architecture, we will default it later
	if prerelease.Architecture != "" {
		if err := validateArchitecture(fmt.Sprintf("%s.architecture", fieldRoot), prerelease.Architecture); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	if prerelease.VersionBounds.Lower == "" {
		validationErrors = append(validationErrors, fmt.Errorf("%s.version_bounds.lower: must be set", fieldRoot))
	}
	if prerelease.VersionBounds.Upper == "" {
		validationErrors = append(validationErrors, fmt.Errorf("%s.version_bounds.upper: must be set", fieldRoot))
	}

	return validationErrors
}
