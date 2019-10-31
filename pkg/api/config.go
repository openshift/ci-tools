package api

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
)

// ValidateAtRuntime validates all the configuration's values without knowledge of config
// repo structure
func (config *ReleaseBuildConfiguration) ValidateAtRuntime() error {
	return config.Validate("", "")
}

// Validate validates all the configuration's values.
func (config *ReleaseBuildConfiguration) Validate(org, repo string) error {
	var validationErrors []error

	validationErrors = append(validationErrors, validateReleaseBuildConfiguration(config, org, repo)...)
	validationErrors = append(validationErrors, validateBuildRootImageConfiguration("build_root", config.InputConfiguration.BuildRootImage, len(config.Images) > 0)...)
	validationErrors = append(validationErrors, validateTestStepConfiguration("tests", config.Tests, config.ReleaseTagConfiguration)...)

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

	// Validate promotion in case of `tag_specification` exists or not
	if config.PromotionConfiguration != nil && config.InputConfiguration.ReleaseTagConfiguration != nil {
		validationErrors = append(validationErrors, validatePromotionWithTagSpec(config.PromotionConfiguration, config.InputConfiguration.ReleaseTagConfiguration)...)
	} else if config.PromotionConfiguration != nil && config.InputConfiguration.ReleaseTagConfiguration == nil {
		validationErrors = append(validationErrors, validatePromotionConfiguration("promotion", *config.PromotionConfiguration)...)
	}

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

func validatePromotionWithTagSpec(promotion *PromotionConfiguration, tagSpec *ReleaseTagConfiguration) []error {
	var validationErrors []error

	if len(promotion.Namespace) == 0 && len(tagSpec.Namespace) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("promotion: no namespace defined"))
	}
	if len(promotion.Name) == 0 && len(promotion.Tag) == 0 {
		if len(tagSpec.Name) != 0 {
			// will get defaulted, is ok
		} else {
			validationErrors = append(validationErrors, errors.New("promotion: no name or tag provided and could not derive defaults from tag_specification"))
		}
	}

	return validationErrors
}

func validateBuildRootImageConfiguration(fieldRoot string, input *BuildRootImageConfiguration, hasImages bool) []error {
	if input == nil {
		if hasImages {
			return []error{errors.New("when 'images' are specified 'build_root' is required and must have image_stream_tag or project_image")}
		}
		return nil
	}

	if input.ProjectImageBuild != nil && input.ImageStreamTagReference != nil {
		return []error{fmt.Errorf("%s: both image_stream_tag and project_image cannot be set", fieldRoot)}
	} else if input.ProjectImageBuild == nil && input.ImageStreamTagReference == nil {
		return []error{fmt.Errorf("%s: you have to specify either image_stream_tag or project_image", fieldRoot)}
	}
	return nil
}

func validateTestStepConfiguration(fieldRoot string, input []TestStepConfiguration, release *ReleaseTagConfiguration) []error {
	var validationErrors []error

	// check for test.As duplicates
	validationErrors = append(validationErrors, searchForTestDuplicates(input)...)

	for num, test := range input {
		fieldRootN := fmt.Sprintf("%s[%d]", fieldRoot, num)
		if len(test.As) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: is required", fieldRootN))
		} else if test.As == "images" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: should not be called 'images' because it gets confused with '[images]' target", fieldRootN))
		} else if ok := regexp.MustCompile("^[a-zA-Z0-9_.-]*$").MatchString(test.As); !ok {
			validationErrors = append(validationErrors, fmt.Errorf("%s.as: '%s' is not valid value, should be [a-zA-Z0-9_.-]", fieldRootN, test.As))
		}
		if hasCommands, hasSteps, hasLiteral := len(test.Commands) != 0, test.MultiStageTestConfiguration != nil, test.MultiStageTestConfigurationLiteral != nil; !hasCommands && !hasSteps && !hasLiteral {
			validationErrors = append(validationErrors, fmt.Errorf("%s: either `commands`, `steps`, or `literal_steps` should be set", fieldRootN))
		} else if hasCommands && (hasSteps || hasLiteral) || (hasSteps && hasLiteral) {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `commands`, `steps`, and `literal_steps` are mutually exclusive", fieldRootN))
		}

		if test.Secret != nil {
			// K8s object names must be valid DNS 1123 subdomains.
			if len(validation.IsDNS1123Subdomain(test.Secret.Name)) != 0 {
				validationErrors = append(validationErrors, fmt.Errorf("%s.name: '%s' secret name is not valid value, should be [a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*", fieldRootN, test.Secret.Name))
			}
			// validate path only if name is passed
			if test.Secret.MountPath != "" {
				if ok := filepath.IsAbs(test.Secret.MountPath); !ok {
					validationErrors = append(validationErrors, fmt.Errorf("%s.path: '%s' secret mount path is not valid value, should be ^((\\/*)\\w+)+", fieldRootN, test.Secret.MountPath))
				}
			}
		}

		validationErrors = append(validationErrors, validateTestConfigurationType(fieldRootN, test, release)...)
	}
	return validationErrors
}

func validateImageStreamTagReference(fieldRoot string, input ImageStreamTagReference) []error {
	var validationErrors []error

	if _, err := url.ParseRequestURI(input.Cluster); err != nil && len(input.Cluster) != 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.cluster invalid URL given: %s", fieldRoot, err))
	}

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
	case ClusterProfileAWS, ClusterProfileAWSAtomic, ClusterProfileAWSCentos, ClusterProfileAWSCentos40, ClusterProfileAWSGluster, ClusterProfileAzure4, ClusterProfileGCP, ClusterProfileGCP40, ClusterProfileGCPHA, ClusterProfileGCPCRIO, ClusterProfileGCPLogging, ClusterProfileGCPLoggingJournald, ClusterProfileGCPLoggingJSONFile, ClusterProfileGCPLoggingCRIO, ClusterProfileOpenStack, ClusterProfileVSphere:
		return nil
	}
	return []error{fmt.Errorf("%q: invalid cluster profile %q", fieldRoot, p)}
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

func validateTestConfigurationType(fieldRoot string, test TestStepConfiguration, release *ReleaseTagConfiguration) []error {
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
		typeCount++
		if testConfig.ClusterProfile != "" && testConfig.Workflow == nil {
			validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
		}
		seen := sets.NewString()
		validationErrors = append(validationErrors, validateTestSteps(fmt.Sprintf("%s.Pre", fieldRoot), testConfig.Pre, seen)...)
		validationErrors = append(validationErrors, validateTestSteps(fmt.Sprintf("%s.Test", fieldRoot), testConfig.Test, seen)...)
		validationErrors = append(validationErrors, validateTestSteps(fmt.Sprintf("%s.Post", fieldRoot), testConfig.Post, seen)...)
	}
	if testConfig := test.MultiStageTestConfigurationLiteral; testConfig != nil {
		typeCount++
		if testConfig.ClusterProfile != "" {
			validationErrors = append(validationErrors, validateClusterProfile(fieldRoot, testConfig.ClusterProfile)...)
		}
		seen := sets.NewString()
		for i, s := range testConfig.Pre {
			fieldRootI := fmt.Sprintf("%s.Pre[%d]", fieldRoot, i)
			validationErrors = append(validationErrors, validateLiteralTestStep(fieldRootI, s, seen)...)
		}
		for i, s := range testConfig.Test {
			fieldRootI := fmt.Sprintf("%s.Test[%d]", fieldRoot, i)
			validationErrors = append(validationErrors, validateLiteralTestStep(fieldRootI, s, seen)...)
		}
		for i, s := range testConfig.Post {
			fieldRootI := fmt.Sprintf("%s.Post[%d]", fieldRoot, i)
			validationErrors = append(validationErrors, validateLiteralTestStep(fieldRootI, s, seen)...)
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

func validateTestSteps(fieldRoot string, steps []TestStep, seen sets.String) (ret []error) {
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
			ret = append(ret, validateLiteralTestStep(fieldRootI, *s.LiteralTestStep, seen)...)
		}
	}
	return
}

func validateLiteralTestStep(fieldRoot string, step LiteralTestStep, seen sets.String) (ret []error) {
	if len(step.As) == 0 {
		ret = append(ret, fmt.Errorf("%s: `as` is required", fieldRoot))
	} else if seen.Has(step.As) {
		ret = append(ret, fmt.Errorf("%s: duplicated name %q", fieldRoot, step.As))
	} else {
		seen.Insert(step.As)
	}
	if len(step.From) == 0 {
		ret = append(ret, fmt.Errorf("%s: `from` is required", fieldRoot))
	}
	if len(step.Commands) == 0 {
		ret = append(ret, fmt.Errorf("%s: `commands` is required", fieldRoot))
	}
	ret = append(ret, validateResourceRequirements(fieldRoot+".resources", step.Resources)...)
	return
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
