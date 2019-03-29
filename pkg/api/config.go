package api

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Validate validates all the configuration's values.
func (config *ReleaseBuildConfiguration) Validate() error {
	var validationErrors []error

	validationErrors = append(validationErrors, validateReleaseBuildConfiguration(config)...)
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

	for i, rawStep := range config.RawSteps {
		if rawStep.PrePublishOutputImageTagStepConfiguration != nil {
			validationErrors = append(validationErrors, validatePrepublishConfiguration(fmt.Sprintf("raw_steps[%d]", i), rawStep.PrePublishOutputImageTagStepConfiguration)...)
		}
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
		if len(test.As) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%d].as: is required", fieldRoot, num))
		} else if test.As == "images" {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%d].as: should not be called 'images' because it gets confused with '[images]' target", fieldRoot, num))
		} else if ok := regexp.MustCompile("^[a-zA-Z0-9_.-]*$").MatchString(test.As); !ok {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%d].as: '%s' is not valid value, should be [a-zA-Z0-9_.-]", fieldRoot, num, test.As))
		}

		if len(test.Commands) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%d].commands: is required", fieldRoot, num))
		}

		if test.Secret != nil {
			// TODO: Move to upstream validation when vendoring is fixed
			// currently checking against DNS RFC 1123 regexp
			if ok := regexp.MustCompile("^[a-z0-9]([-a-z0-9]*[a-z0-9])?$").MatchString(test.Secret.Name); !ok {
				validationErrors = append(validationErrors, fmt.Errorf("%s[%d].name: '%s' secret name is not valid value, should be [a-z0-9]([-a-z0-9]*[a-z0-9]", fieldRoot, num, test.Secret.Name))
			}
			// validate path only if name is passed
			if test.Secret.MountPath != "" {
				if ok := filepath.IsAbs(test.Secret.MountPath); !ok {
					validationErrors = append(validationErrors, fmt.Errorf("%s[%d].path: '%s' secret mount path is not valid value, should be ^((\\/*)\\w+)+", fieldRoot, num, test.Secret.MountPath))
				}
			}
		}

		validationErrors = append(validationErrors, validateTestConfigurationType(fmt.Sprintf("%s[%d]", fieldRoot, num), test, release)...)
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
	case ClusterProfileAWS, ClusterProfileAWSAtomic, ClusterProfileAWSCentos, ClusterProfileAWSCentos40, ClusterProfileAWSGluster, ClusterProfileGCP, ClusterProfileGCP40, ClusterProfileGCPHA, ClusterProfileGCPCRIO, ClusterProfileGCPLogging, ClusterProfileGCPLoggingJournald, ClusterProfileGCPLoggingJSONFile, ClusterProfileGCPLoggingCRIO, ClusterProfileOpenStack, ClusterProfileVSphere:
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
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftAnsibleSrcClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftAnsibleCustomClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftAnsible40ClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftAnsibleUpgradeClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerSrcClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile)...)
	}
	if testConfig := test.OpenshiftInstallerUPIClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile)...)
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

func validateReleaseBuildConfiguration(input *ReleaseBuildConfiguration) []error {
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

	validationErrors = append(validationErrors, validateResources("resources", input.Resources)...)
	return validationErrors
}

func validatePrepublishConfiguration(fieldRoot string, input *PrePublishOutputImageTagStepConfiguration) []error {
	var validationErrors []error

	if len(input.From) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.from: no from image defined", fieldRoot))
	}

	if len(input.To.Namespace) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.to: no namespace defined", fieldRoot))
	}

	if len(input.To.Name) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.to: no name defined", fieldRoot))
	}
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
