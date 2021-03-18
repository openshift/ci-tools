package validation

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

// ValidateAtRuntime validates all the configuration's values without knowledge of config
// repo structure
func IsValidRuntimeConfiguration(config *api.ReleaseBuildConfiguration) error {
	return validateConfiguration(config, "", "", false)
}

// ValidateResolved behaves as ValidateAtRuntime and also validates that all
// test steps are fully resolved.
func IsValidResolvedConfiguration(config *api.ReleaseBuildConfiguration) error {
	config.Default()
	return validateConfiguration(config, "", "", true)
}

// Validate validates all the configuration's values.
func IsValidConfiguration(config *api.ReleaseBuildConfiguration, org, repo string) error {
	config.Default()
	return validateConfiguration(config, org, repo, false)
}

func validateConfiguration(config *api.ReleaseBuildConfiguration, org, repo string, resolved bool) error {
	var validationErrors []error

	validationErrors = append(validationErrors, validateReleaseBuildConfiguration(config, org, repo)...)
	validationErrors = append(validationErrors, validateBuildRootImageConfiguration("build_root", config.InputConfiguration.BuildRootImage, len(config.Images) > 0))
	releases := sets.NewString()
	for name := range releases {
		releases.Insert(name)
	}
	validationErrors = append(validationErrors, validateTestStepConfiguration("tests", config.Tests, config.ReleaseTagConfiguration, releases, resolved)...)

	// this validation brings together a large amount of data from separate
	// parts of the configuration, so it's written as a standalone method
	validationErrors = append(validationErrors, validateTestStepDependencies(config)...)

	if config.Images != nil {
		validationErrors = append(validationErrors, validateImages("images", config.Images)...)
	}

	if config.Operator != nil {
		// validateOperator needs a method that maps `substitute.with` values to image links
		// to validate the value is meaningful in the context of the configuration
		linkForImage := func(image string) api.StepLink {
			imageStream, name, _ := config.DependencyParts(api.StepDependency{Name: image})
			return api.LinkForImage(imageStream, name)
		}
		validationErrors = append(validationErrors, validateOperator("operator", config.Operator, linkForImage, config)...)
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

func validateBuildRootImageConfiguration(fieldRoot string, input *api.BuildRootImageConfiguration, hasImages bool) error {
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

func validateImages(fieldRoot string, input []api.ProjectDirectoryImageBuildStepConfiguration) []error {
	var validationErrors []error
	seenNames := map[api.PipelineImageStreamTagReference]int{}
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
		if image.To == api.PipelineImageStreamTagReferenceBundleSource {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `to` cannot be %s", fieldRootN, api.PipelineImageStreamTagReferenceBundleSource))
		}
		if strings.HasPrefix(string(image.To), api.BundlePrefix) {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `to` cannot begin with `%s`", fieldRootN, api.BundlePrefix))
		}
		if strings.HasPrefix(string(image.To), string(api.PipelineImageStreamTagReferenceIndexImage)) {
			validationErrors = append(validationErrors, fmt.Errorf("%s: `to` cannot begin with %s", fieldRootN, api.PipelineImageStreamTagReferenceIndexImage))
		}
		if image.DockerfileLiteral != nil && (image.ContextDir != "" || image.DockerfilePath != "") {
			validationErrors = append(validationErrors, fmt.Errorf("%s: dockerfile_literal is mutually exclusive with context_dir and dockerfile_path", fieldRootN))
		}
	}
	return validationErrors
}

func validateOperator(fieldRoot string, input *api.OperatorStepConfiguration, linkForImage func(string) api.StepLink, config *api.ReleaseBuildConfiguration) []error {
	var validationErrors []error
	for num, bundle := range input.Bundles {
		fieldRootN := fmt.Sprintf("%s.bundles[%d]", fieldRoot, num)
		if bundle.As != "" {
			if config.IsBaseImage(bundle.As) {
				validationErrors = append(validationErrors, fmt.Errorf("%s.as: bundle name `%s` matches a base image", fieldRootN, bundle.As))
			}
			if config.BuildsImage(bundle.As) {
				validationErrors = append(validationErrors, fmt.Errorf("%s.as: bundle name `%s` matches image defined in `images`", fieldRootN, bundle.As))
			}
		}
		if bundle.As == "" && bundle.BaseIndex != "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.base_index: base_index requires as to be set", fieldRootN))
		}
		if bundle.UpdateGraph != "" {
			if bundle.BaseIndex == "" {
				validationErrors = append(validationErrors, fmt.Errorf("%s.update_graph: update_graph requires base_index to be set", fieldRootN))
			}
			if bundle.UpdateGraph != api.IndexUpdateSemver && bundle.UpdateGraph != api.IndexUpdateSemverSkippatch && bundle.UpdateGraph != api.IndexUpdateReplaces {
				validationErrors = append(validationErrors, fmt.Errorf("%s.update_graph: update_graph must be %s, %s, or %s", fieldRootN, api.IndexUpdateSemver, api.IndexUpdateSemverSkippatch, api.IndexUpdateReplaces))
			}
		}
	}
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

func validateImageStreamTagReference(fieldRoot string, input api.ImageStreamTagReference) []error {
	var validationErrors []error

	if len(input.Tag) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.tag: value required but not provided", fieldRoot))
	}

	return validationErrors
}

func validateImageStreamTagReferenceMap(fieldRoot string, input map[string]api.ImageStreamTagReference) []error {
	var validationErrors []error
	for k, v := range input {
		if k == "root" {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s can't be named 'root'", fieldRoot, k))
		}
		if k == string(api.PipelineImageStreamTagReferenceBundleSource) {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s: cannot be named %s", fieldRoot, k, api.PipelineImageStreamTagReferenceBundleSource))
		}
		if strings.HasPrefix(k, api.BundlePrefix) {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s: cannot begin with `%s`", fieldRoot, k, api.BundlePrefix))
		}
		if strings.HasPrefix(k, string(api.PipelineImageStreamTagReferenceIndexImage)) {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s: cannot begin with %s", fieldRoot, k, api.PipelineImageStreamTagReferenceIndexImage))
		}
		validationErrors = append(validationErrors, validateImageStreamTagReference(fmt.Sprintf("%s.%s", fieldRoot, k), v)...)
	}
	return validationErrors
}

func validatePromotionConfiguration(fieldRoot string, input api.PromotionConfiguration) []error {
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

func validateReleaseTagConfiguration(fieldRoot string, input api.ReleaseTagConfiguration) []error {
	var validationErrors []error

	if len(input.Namespace) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no namespace defined", fieldRoot))
	}

	if len(input.Name) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: no name defined", fieldRoot))
	}

	return validationErrors
}

func validateReleaseBuildConfiguration(input *api.ReleaseBuildConfiguration, org, repo string) []error {
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

func validateResources(fieldRoot string, resources api.ResourceConfiguration) []error {
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

func validateResourceRequirements(fieldRoot string, requirements api.ResourceRequirements) []error {
	var validationErrors []error

	validationErrors = append(validationErrors, validateResourceList(fmt.Sprintf("%s.limits", fieldRoot), requirements.Limits)...)
	validationErrors = append(validationErrors, validateResourceList(fmt.Sprintf("%s.requests", fieldRoot), requirements.Requests)...)

	if len(requirements.Requests) == 0 && len(requirements.Limits) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("'%s' should have at least one request or limit", fieldRoot))
	}

	return validationErrors
}

func validateResourceList(fieldRoot string, list api.ResourceList) []error {
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
		case "devices.kubevirt.io/kvm":
			v := list[key]
			if v != "1" {
				validationErrors = append(validationErrors, fmt.Errorf("%s.%s: must be 1", fieldRoot, key))
			}
		default:
			numInvalid++
			validationErrors = append(validationErrors, fmt.Errorf("'%s' specifies an invalid key %s", fieldRoot, key))
		}
	}

	return validationErrors
}
