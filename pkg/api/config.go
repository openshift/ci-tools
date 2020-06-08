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
	validationErrors = append(validationErrors, validateBuildRootImageConfiguration("build_root", config.InputConfiguration.BuildRootImage, len(config.Images) > 0)...)
	validationErrors = append(validationErrors, validateTestStepConfiguration("tests", config.Tests, config.ReleaseTagConfiguration, resolved)...)

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

func validateBuildRootImageConfiguration(fieldRoot string, input *BuildRootImageConfiguration, hasImages bool) []error {
	if input == nil {
		if hasImages {
			return []error{errors.New("when 'images' are specified 'build_root' is required and must have image_stream_tag or project_image")}
		}
		return nil
	}

	var validationErrors []error
	if input.ProjectImageBuild != nil && input.ImageStreamTagReference != nil {
		validationErrors = append(validationErrors, fmt.Errorf("%s: both image_stream_tag and project_image cannot be set", fieldRoot))
	} else if input.ProjectImageBuild == nil && input.ImageStreamTagReference == nil {
		validationErrors = append(validationErrors, fmt.Errorf("%s: you have to specify either image_stream_tag or project_image", fieldRoot))
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
		} else if len(validation.IsDNS1123Subdomain(test.As)) != 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: '%s' is not a valid Kubernetes object name", fieldRootN, test.As))
		}
		if hasCommands, hasSteps, hasLiteral := len(test.Commands) != 0, test.MultiStageTestConfiguration != nil, test.MultiStageTestConfigurationLiteral != nil; !hasCommands && !hasSteps && !hasLiteral {
			validationErrors = append(validationErrors, fmt.Errorf("%s: either `commands`, `steps`, or `literal_steps` should be set", fieldRootN))
		} else if hasCommands && (hasSteps || hasLiteral) || (hasSteps && hasLiteral) {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `commands`, `steps`, and `literal_steps` are mutually exclusive", fieldRootN))
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
		if len(v.Cluster) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%s]: no cluster defined", fieldRoot, k))
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
	case ClusterProfileAWS, ClusterProfileAWSAtomic, ClusterProfileAWSCentos, ClusterProfileAWSCentos40, ClusterProfileAWSGluster, ClusterProfileAzure4, ClusterProfileGCP, ClusterProfileGCP40, ClusterProfileGCPHA, ClusterProfileGCPCRIO, ClusterProfileGCPLogging, ClusterProfileGCPLoggingJournald, ClusterProfileGCPLoggingJSONFile, ClusterProfileGCPLoggingCRIO, ClusterProfileLibvirtPpc64le, ClusterProfileLibvirtS390x, ClusterProfileOpenStack, ClusterProfileOpenStackVexxhost, ClusterProfileOpenStackPpc64le, ClusterProfileOvirt, ClusterProfilePacket, ClusterProfileVSphere:
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
				validationErrors = append(validationErrors, fmt.Errorf("%s.memory_backed_volume: 'size' must be a Kubernetes quantity: %v", fieldRoot, err))
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
	if testConfig := test.OpenshiftAnsibleUpgradeClusterTestConfiguration; testConfig != nil {
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
	if testConfig := test.OpenshiftInstallerConsoleClusterTestConfiguration; testConfig != nil {
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
		if testConfig.Workflow == nil && testConfig.ClusterProfile != "" {
			validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
		}
		seen := sets.NewString()
		validationErrors = append(validationErrors, validateTestSteps(fmt.Sprintf("%s.Pre", fieldRoot), testConfig.Pre, seen, testConfig.Environment)...)
		validationErrors = append(validationErrors, validateTestSteps(fmt.Sprintf("%s.Test", fieldRoot), testConfig.Test, seen, testConfig.Environment)...)
		validationErrors = append(validationErrors, validateTestSteps(fmt.Sprintf("%s.Post", fieldRoot), testConfig.Post, seen, testConfig.Environment)...)
	}
	if testConfig := test.MultiStageTestConfigurationLiteral; testConfig != nil {
		typeCount++
		if testConfig.ClusterProfile != "" {
			validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
		}
		seen := sets.NewString()
		for i, s := range testConfig.Pre {
			fieldRootI := fmt.Sprintf("%s.Pre[%d]", fieldRoot, i)
			validationErrors = append(validationErrors, validateLiteralTestStep(fieldRootI, s, seen, testConfig.Environment)...)
		}
		for i, s := range testConfig.Test {
			fieldRootI := fmt.Sprintf("%s.Test[%d]", fieldRoot, i)
			validationErrors = append(validationErrors, validateLiteralTestStep(fieldRootI, s, seen, testConfig.Environment)...)
		}
		for i, s := range testConfig.Post {
			fieldRootI := fmt.Sprintf("%s.Post[%d]", fieldRoot, i)
			validationErrors = append(validationErrors, validateLiteralTestStep(fieldRootI, s, seen, testConfig.Environment)...)
		}
	}
	if test.OpenshiftInstallerRandomClusterTestConfiguration != nil {
		typeCount++
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

func validateTestSteps(fieldRoot string, steps []TestStep, seen sets.String, env TestEnvironment) (ret []error) {
	for i, s := range steps {
		fieldRootI := fmt.Sprintf("%s[%d]", fieldRoot, i)
		if (s.LiteralTestStep != nil && s.Reference != nil) ||
			(s.LiteralTestStep != nil && s.Chain != nil) ||
			(s.Reference != nil && s.Chain != nil) {
			ret = append(ret, fmt.Errorf("%s: only one of `ref`, `chain`, or a literal test step can be set", fieldRootI))
			continue
		}
		if s.LiteralTestStep == nil && s.Reference == nil && s.Chain == nil {
			ret = append(ret, fmt.Errorf("%s: a reference, chain, or literal test step is required", fieldRootI))
			continue
		}
		if s.Reference != nil {
			if len(*s.Reference) == 0 {
				ret = append(ret, fmt.Errorf("%s.ref: length cannot be 0", fieldRootI))
			} else if seen.Has(*s.Reference) {
				ret = append(ret, fmt.Errorf("%s.ref: duplicated name %q", fieldRootI, *s.Reference))
			} else {
				seen.Insert(*s.Reference)
			}
		}
		if s.Chain != nil {
			if len(*s.Chain) == 0 {
				ret = append(ret, fmt.Errorf("%s.chain: length cannot be 0", fieldRootI))
			} else if seen.Has(*s.Chain) {
				ret = append(ret, fmt.Errorf("%s.chain: duplicated name %q", fieldRootI, *s.Chain))
			} else {
				seen.Insert(*s.Chain)
			}
		}
		if s.LiteralTestStep != nil {
			ret = append(ret, validateLiteralTestStep(fieldRootI, *s.LiteralTestStep, seen, env)...)
		}
	}
	return
}

func validateLiteralTestStep(fieldRoot string, step LiteralTestStep, seen sets.String, env TestEnvironment) (ret []error) {
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
	} else if len(validation.IsDNS1123Subdomain(step.From)) != 0 {
		ret = append(ret, fmt.Errorf("%s.from: '%s' is not a valid Kubernetes object name", fieldRoot, step.From))
	}
	if len(step.Commands) == 0 {
		ret = append(ret, fmt.Errorf("%s: `commands` is required", fieldRoot))
	}
	ret = append(ret, validateResourceRequirements(fieldRoot+".resources", step.Resources)...)
	ret = append(ret, validateCredentials(fieldRoot, step.Credentials)...)
	if err := validateParameters(fieldRoot, step.Environment, env); err != nil {
		ret = append(ret, err)
	}
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
				errs = append(errs, fmt.Errorf("%s.credentials[%d] could not check relative path to credentials[%d] (%s)", fieldRoot, i, index, err))
				continue
			}
			if !strings.Contains(relPath, "..") {
				errs = append(errs, fmt.Errorf("%s.credentials[%d] mounts at %s, which is under credentials[%d] (%s)", fieldRoot, i, credential.MountPath, index, other.MountPath))
			}
			relPath, err = filepath.Rel(credential.MountPath, other.MountPath)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s.credentials[%d] could not check relative path to credentials[%d] (%s)", fieldRoot, index, i, err))
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
		if param.Default != "" {
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
				validationErrors = append(validationErrors, fmt.Errorf("%s.%s: invalid quantity: %v", fieldRoot, key, err))
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
