package api

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
)

var originReleaseTagRegexp = regexp.MustCompile(`^origin-v\d+\.\d+$`)

// Validate validates all the configuration's values.
func (config *ReleaseBuildConfiguration) Validate() error {
	var validationErrors []error

	validationErrors = append(validationErrors, validateReleaseBuildConfiguration(config))
	validationErrors = append(validationErrors, validateBuildRootImageConfiguration("build_root", config.InputConfiguration.BuildRootImage))
	validationErrors = append(validationErrors, validateTestStepConfiguration("tests", config.Tests, config.ReleaseTagConfiguration))

	if config.InputConfiguration.BaseImages != nil {
		validationErrors = append(validationErrors, validateImageStreamTagReferenceMap("base_images", config.InputConfiguration.BaseImages))
	}

	if config.InputConfiguration.BaseRPMImages != nil {
		validationErrors = append(validationErrors, validateImageStreamTagReferenceMap("base_rpm_images", config.InputConfiguration.BaseRPMImages))
	}

	// Validate tag_specification
	if config.InputConfiguration.ReleaseTagConfiguration != nil {
		validationErrors = append(validationErrors, validateReleaseTagConfiguration("tag_specification", *config.InputConfiguration.ReleaseTagConfiguration))
	}

	// Validate promotion in case of `tag_specification` exists or not
	if config.PromotionConfiguration != nil && config.InputConfiguration.ReleaseTagConfiguration != nil {
		validationErrors = append(validationErrors, validatePromotionWithTagSpec(config.PromotionConfiguration, config.InputConfiguration.ReleaseTagConfiguration))
	} else if config.PromotionConfiguration != nil && config.InputConfiguration.ReleaseTagConfiguration == nil {
		validationErrors = append(validationErrors, validatePromotionConfiguration("promotion", *config.PromotionConfiguration))
	}

	return kerrors.NewAggregate(validationErrors)
}

func validatePromotionWithTagSpec(promotion *PromotionConfiguration, tagSpec *ReleaseTagConfiguration) error {
	var validationErrors []error

	if len(promotion.Namespace) == 0 && len(tagSpec.Namespace) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("promotion: no namespace defined"))
	}
	if len(promotion.Name) == 0 && len(promotion.Tag) == 0 {
		if len(tagSpec.Name) != 0 || len(tagSpec.Tag) != 0 {
			// will get defaulted, is ok
		} else {
			validationErrors = append(validationErrors, errors.New("promotion: no name or tag provided and could not derive defaults from tag_specification"))
		}
	}

	return kerrors.NewAggregate(validationErrors)
}

func validateBuildRootImageConfiguration(fieldRoot string, input *BuildRootImageConfiguration) error {
	if input == nil {
		return errors.New("`build_root` is required")
	}

	if input.ProjectImageBuild != nil && input.ImageStreamTagReference != nil {
		return fmt.Errorf("%s: both image_stream_tag and project_image_build cannot be set", fieldRoot)
	} else if input.ProjectImageBuild == nil && input.ImageStreamTagReference == nil {
		return fmt.Errorf("%s: you have to specify either image_stream_tag or project_image_build", fieldRoot)
	}
	return nil
}

func validateTestStepConfiguration(fieldRoot string, input []TestStepConfiguration, release *ReleaseTagConfiguration) error {
	var validationErrors []error

	// check for test.As duplicates
	validationErrors = append(validationErrors, searchForTestDuplicates(input))

	for num, test := range input {
		if len(test.As) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%d].as: is required", fieldRoot, num))
		} else if test.As == "images" {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%d].as: should not be called 'images' because it gets confused with '[images]' target", fieldRoot, num))
		} else if ok := regexp.MustCompile("^[a-zA-Z0-9_.-]*$").MatchString(test.As); !ok {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%d].as: `%s` is not valid value, should be [a-zA-Z0-9_.-]", fieldRoot, num, test.As))
		}

		if len(test.Commands) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%d].commands: is required", fieldRoot, num))
		}

		validationErrors = append(validationErrors, validateTestConfigurationType(fmt.Sprintf("%s[%d]", fieldRoot, num), test, release))
	}
	return kerrors.NewAggregate(validationErrors)
}

func validateImageStreamTagReference(fieldRoot string, input ImageStreamTagReference) error {
	var validationErrors []error

	if _, err := url.ParseRequestURI(input.Cluster); err != nil && len(input.Cluster) != 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.cluster invalid URL given: %s", fieldRoot, err))
	}

	if len(input.Tag) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.tag: value required but not provided", fieldRoot))
	}

	return kerrors.NewAggregate(validationErrors)
}

func validateImageStreamTagReferenceMap(fieldRoot string, input map[string]ImageStreamTagReference) error {
	var validationErrors []error
	for k, v := range input {
		if k == "root" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s can't be named `root`", fieldRoot, k))
		}
		validationErrors = append(validationErrors, validateImageStreamTagReference(fmt.Sprintf("%s.%s", fieldRoot, k), v))
	}
	return kerrors.NewAggregate(validationErrors)
}

func validatePromotionConfiguration(fieldRoot string, input PromotionConfiguration) error {
	var validationErrors []error

	if len(input.Namespace) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no namespace defined", fieldRoot))
	}

	if len(input.Name) == 0 && len(input.Tag) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no name or tag defined", fieldRoot))
	}
	return kerrors.NewAggregate(validationErrors)
}

func validateReleaseTagConfiguration(fieldRoot string, input ReleaseTagConfiguration) error {
	var validationErrors []error
	if len(input.Cluster) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no cluster defined", fieldRoot))
	}

	if len(input.Namespace) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no namespace defined", fieldRoot))
	}

	if len(input.Name) == 0 && len(input.Tag) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no name or tag defined", fieldRoot))
	}
	return kerrors.NewAggregate(validationErrors)
}

func validateClusterProfile(fieldRoot string, p ClusterProfile) error {
	switch p {
	case ClusterProfileAWS, ClusterProfileAWSAtomic, ClusterProfileAWSCentos, ClusterProfileGCP, ClusterProfileGCPHA, ClusterProfileGCPCRIO:
		return nil
	}
	return fmt.Errorf("%q: invalid cluster profile %q", fieldRoot, p)
}

func searchForTestDuplicates(tests []TestStepConfiguration) error {
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
		return fmt.Errorf("tests: found duplicated test: (%s)", strings.Join(testNames, ","))
	}
	return nil
}

func validateTestConfigurationType(fieldRoot string, test TestStepConfiguration, release *ReleaseTagConfiguration) error {
	var validationErrors []error
	typeCount := 0
	if testConfig := test.ContainerTestConfiguration; testConfig != nil {
		typeCount++
		if len(testConfig.From) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `from` is required", fieldRoot))
		}
	}
	var needsReleaseRpms bool
	if testConfig := test.OpenshiftAnsibleClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile))
	}
	if testConfig := test.OpenshiftAnsibleSrcClusterTestConfiguration; testConfig != nil {
		typeCount++
		needsReleaseRpms = true
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile))
	}
	if testConfig := test.OpenshiftInstallerClusterTestConfiguration; testConfig != nil {
		typeCount++
		validationErrors = append(validationErrors, validateClusterProfile(fmt.Sprintf("%s", fieldRoot), testConfig.ClusterProfile))
	}
	if typeCount == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s has no type", fieldRoot))
	} else if typeCount == 1 {
		if needsReleaseRpms && (release == nil || !originReleaseTagRegexp.MatchString(release.Name)) {
			validationErrors = append(validationErrors, fmt.Errorf("%s requires an 'origin' release in `tag_specification`", fieldRoot))
		}
	} else if typeCount > 1 {
		validationErrors = append(validationErrors, fmt.Errorf("%s has more than one type", fieldRoot))
	}

	return kerrors.NewAggregate(validationErrors)
}

func validateReleaseBuildConfiguration(input *ReleaseBuildConfiguration) error {
	var validationErrors []error

	if input.Tests == nil && input.Images == nil {
		validationErrors = append(validationErrors, errors.New("both `tests` and `images` are not defined"))
	}

	if len(input.RpmBuildLocation) != 0 && len(input.RpmBuildCommands) == 0 {
		validationErrors = append(validationErrors, errors.New("`rpm_build_location` defined but no `rpm_build_commands` found"))
	}

	if input.BaseRPMImages != nil && len(input.RpmBuildCommands) == 0 {
		validationErrors = append(validationErrors, errors.New("`base_rpm_images` defined but no `rpm_build_commands` found"))
	}

	if input.Resources == nil {
		validationErrors = append(validationErrors, errors.New("`resources` cannot be empty, at least the blanket `*` has to be specified"))
	}

	return kerrors.NewAggregate(validationErrors)
}
