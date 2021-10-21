package validation

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

// Validator holds data used across validations.
type Validator struct {
	// hasTrapCache avoids redundant regexp searches on step commands.
	hasTrapCache map[string]bool
}

// NewValidator creates an object that optimizes bulk validations.
func NewValidator() Validator {
	return Validator{
		hasTrapCache: make(map[string]bool),
	}
}

func newSingleUseValidator() Validator {
	return Validator{}
}

func (v *Validator) IsValidRuntimeConfiguration(config *api.ReleaseBuildConfiguration) error {
	return v.validateConfiguration(NewConfigContext(), config, "", "", false)
}

// IsValidResolvedConfiguration behaves as ValidateAtRuntime and also validates that all
// test steps are fully resolved.
func (v *Validator) IsValidResolvedConfiguration(config *api.ReleaseBuildConfiguration) error {
	config.Default()
	return v.validateConfiguration(NewConfigContext(), config, "", "", true)
}

// IsValidConfiguration validates all the configuration's values.
func (v *Validator) IsValidConfiguration(config *api.ReleaseBuildConfiguration, org, repo string) error {
	config.Default()
	return v.validateConfiguration(NewConfigContext(), config, org, repo, false)
}

// configContext contains data structures used for validations across fields.
type configContext struct {
	field fieldPath
	// Shared reference to a map containing pipeline image tags seen so far.
	// All derivative contexts will point to the same map.
	pipelineImages map[api.PipelineImageStreamTagReference]string
}

// newConfigContext creates a top-level, empty context.
func NewConfigContext() *configContext {
	return &configContext{
		pipelineImages: make(map[api.PipelineImageStreamTagReference]string),
	}
}

func (c *configContext) errorf(format string, args ...interface{}) error {
	return c.field.errorf(format, args...)
}

func (c *configContext) AddField(name string) *configContext {
	ret := *c
	ret.field = ret.field.addField(name)
	return &ret
}

func (c *configContext) addIndex(i int) *configContext {
	ret := *c
	ret.field = ret.field.addIndex(i)
	return &ret
}

func (c *configContext) addKey(k string) *configContext {
	ret := *c
	ret.field = ret.field.addKey(k)
	return &ret
}

// addPipelineImage verifies that a pipeline image tag has not been seen.
// An error containing the name of the original field is returned if the tag has
// already been seen in the same configuration.
func (c *configContext) addPipelineImage(name api.PipelineImageStreamTagReference) error {
	previous, seen := c.pipelineImages[name]
	if seen {
		return c.errorf("duplicate image name '%s' (previously defined by field '%s')", string(name), previous)
	}
	c.pipelineImages[name] = string(c.field)
	return nil
}

// ValidateAtRuntime validates all the configuration's values without knowledge of config
// repo structure
func IsValidRuntimeConfiguration(config *api.ReleaseBuildConfiguration) error {
	v := newSingleUseValidator()
	return v.validateConfiguration(NewConfigContext(), config, "", "", false)
}

// ValidateResolved behaves as ValidateAtRuntime and also validates that all
// test steps are fully resolved.
func IsValidResolvedConfiguration(config *api.ReleaseBuildConfiguration) error {
	config.Default()
	v := newSingleUseValidator()
	return v.validateConfiguration(NewConfigContext(), config, "", "", true)
}

// Validate validates all the configuration's values.
func IsValidConfiguration(config *api.ReleaseBuildConfiguration, org, repo string) error {
	config.Default()
	v := newSingleUseValidator()
	return v.validateConfiguration(NewConfigContext(), config, org, repo, false)
}

func (v *Validator) validateConfiguration(ctx *configContext, config *api.ReleaseBuildConfiguration, org, repo string, resolved bool) error {
	var validationErrors []error
	if config.BinaryBuildCommands != "" {
		ctx.pipelineImages[api.PipelineImageStreamTagReferenceBinaries] = "binary_build_commands"
	}
	if config.TestBinaryBuildCommands != "" {
		ctx.pipelineImages[api.PipelineImageStreamTagReferenceTestBinaries] = "test_binary_build_commands"
	}
	if config.RpmBuildCommands != "" {
		ctx.pipelineImages[api.PipelineImageStreamTagReferenceRPMs] = "rpm_build_commands"
	}
	validationErrors = append(validationErrors, validateReleaseBuildConfiguration(config, org, repo)...)
	validationErrors = append(validationErrors, validateBuildRootImageConfiguration(ctx.AddField("build_root"), config.InputConfiguration.BuildRootImage, len(config.Images) > 0)...)

	if config.Operator != nil {
		validationErrors = append(validationErrors, ValidateOperator(ctx.AddField("operator"), config)...)
	}
	validationErrors = append(validationErrors, ValidateBaseImages(ctx.AddField("base_images"), config.InputConfiguration.BaseImages)...)
	validationErrors = append(validationErrors, validateBaseRPMImages(ctx.AddField("base_rpm_images"), config.InputConfiguration.BaseRPMImages)...)
	// Validate tag_specification
	if config.InputConfiguration.ReleaseTagConfiguration != nil {
		validationErrors = append(validationErrors, validateReleaseTagConfiguration("tag_specification", *config.InputConfiguration.ReleaseTagConfiguration)...)
	}

	// Validate promotion
	if config.PromotionConfiguration != nil {
		validationErrors = append(validationErrors, validatePromotionConfiguration("promotion", *config.PromotionConfiguration)...)
	}

	validationErrors = append(validationErrors, validateReleases("releases", config.Releases, config.ReleaseTagConfiguration != nil)...)
	validationErrors = append(validationErrors, ValidateImages(ctx.AddField("images"), config.Images)...)
	validationErrors = append(validationErrors, v.ValidateTestStepConfiguration(ctx, config, resolved)...)
	// this validation brings together a large amount of data from separate
	// parts of the configuration, so it's written as a standalone method
	validationErrors = append(validationErrors, validateTestStepDependencies(config)...)
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

func (v *Validator) ValidateTestStepConfiguration(ctx *configContext, config *api.ReleaseBuildConfiguration, resolved bool) []error {
	var validationErrors []error

	releases := sets.NewString()
	for name := range config.Releases {
		releases.Insert(name)
	}

	if tests := config.Tests; len(tests) != 0 {
		images := sets.NewString()
		for _, i := range config.Images {
			images.Insert(string(i.To))
		}
		validationErrors = append(validationErrors, v.validateTestStepConfiguration(ctx, "tests", config.Tests, config.ReleaseTagConfiguration, releases, images, resolved)...)
	}
	return validationErrors
}

func validateBuildRootImageConfiguration(ctx *configContext, input *api.BuildRootImageConfiguration, hasImages bool) (ret []error) {
	if input == nil {
		if hasImages {
			return []error{errors.New("when 'images' are specified 'build_root' is required and must have image_stream_tag, project_image or from_repository set")}
		}
		return nil
	}

	if input.ProjectImageBuild != nil && input.ImageStreamTagReference != nil {
		ret = append(ret, ctx.errorf("image_stream_tag and project_image are mutually exclusive"))
	} else if input.ProjectImageBuild != nil && input.FromRepository {
		ret = append(ret, ctx.errorf("project_image and from_repository are mutually exclusive"))
	} else if input.FromRepository && input.ImageStreamTagReference != nil {
		ret = append(ret, ctx.errorf("from_repository and image_stream_tag are mutually exclusive"))
	} else if input.ProjectImageBuild == nil && input.ImageStreamTagReference == nil && !input.FromRepository {
		ret = append(ret, ctx.errorf("you have to specify one of project_image, image_stream_tag or from_repository"))
	} else if input.ImageStreamTagReference != nil {
		ret = append(ret, validateBuildRootImageStreamTag(ctx.AddField("image_stream_tag"), *input.ImageStreamTagReference)...)
	}
	if err := ctx.addPipelineImage(api.PipelineImageStreamTagReferenceRoot); err != nil {
		ret = append(ret, err)
	}
	return
}

func validateBuildRootImageStreamTag(ctx *configContext, buildRoot api.ImageStreamTagReference) []error {
	var validationErrors []error
	if len(buildRoot.Namespace) == 0 {
		validationErrors = append(validationErrors, ctx.AddField("namespace").errorf("value required but not provided"))
	}
	if len(buildRoot.Name) == 0 {
		validationErrors = append(validationErrors, ctx.AddField("name").errorf("value required but not provided"))
	}
	if len(buildRoot.Tag) == 0 {
		validationErrors = append(validationErrors, ctx.AddField("tag").errorf("value required but not provided"))
	}
	return validationErrors
}

func ValidateImages(ctx *configContext, images []api.ProjectDirectoryImageBuildStepConfiguration) []error {
	var validationErrors []error
	for num, image := range images {
		ctxN := ctx.addIndex(num)
		if image.To == "" {
			validationErrors = append(validationErrors, ctxN.errorf("`to` must be set"))
		}
		if err := ctxN.addPipelineImage(image.To); err != nil {
			validationErrors = append(validationErrors, err)
		}
		if image.DockerfileLiteral != nil && (image.ContextDir != "" || image.DockerfilePath != "") {
			validationErrors = append(validationErrors, ctxN.errorf("dockerfile_literal is mutually exclusive with context_dir and dockerfile_path"))
		}
	}
	return validationErrors
}

func ValidateOperator(ctx *configContext, config *api.ReleaseBuildConfiguration) []error {
	// validateOperator needs a method that maps `substitute.with` values to image links
	// to validate the value is meaningful in the context of the configuration
	linkForImage := func(image string) api.StepLink {
		return LinkForImage(image, config)
	}

	return validateOperator(ctx, config.Operator, linkForImage)
}

func LinkForImage(image string, config *api.ReleaseBuildConfiguration) api.StepLink {
	imageStream, name, _ := config.DependencyParts(api.StepDependency{Name: image}, nil)
	return api.LinkForImage(imageStream, name)
}

func validateOperator(ctx *configContext, input *api.OperatorStepConfiguration, linkForImage func(string) api.StepLink) []error {
	var validationErrors []error
	if err := ctx.addPipelineImage(api.PipelineImageStreamTagReferenceBundleSource); err != nil {
		validationErrors = append(validationErrors, err)
	}
	for num, bundle := range input.Bundles {
		ctxN := ctx.AddField("bundles").addIndex(num)
		ctxImage := ctxN
		imageName := bundle.As
		if imageName == "" {
			imageName = api.BundleName(num)
		} else {
			ctxImage = ctxN.AddField("as")
		}
		if err := ctxImage.addPipelineImage(api.PipelineImageStreamTagReference(imageName)); err != nil {
			validationErrors = append(validationErrors, err)
		}
		if err := ctxImage.addPipelineImage(api.PipelineImageStreamTagReference(api.IndexName(imageName))); err != nil {
			validationErrors = append(validationErrors, err)
		}
		if bundle.As == "" && bundle.BaseIndex != "" {
			validationErrors = append(validationErrors, ctxN.AddField("base_index").errorf("base_index requires as to be set"))
		}
		if bundle.UpdateGraph != "" {
			if bundle.BaseIndex == "" {
				validationErrors = append(validationErrors, ctxN.AddField("update_graph").errorf("update_graph requires base_index to be set"))
			}
			if bundle.UpdateGraph != api.IndexUpdateSemver && bundle.UpdateGraph != api.IndexUpdateSemverSkippatch && bundle.UpdateGraph != api.IndexUpdateReplaces {
				validationErrors = append(validationErrors, ctxN.AddField("update_graph").errorf("update_graph must be %s, %s, or %s", api.IndexUpdateSemver, api.IndexUpdateSemverSkippatch, api.IndexUpdateReplaces))
			}
		}
	}
	for num, sub := range input.Substitutions {
		ctxN := ctx.AddField("substitute").addIndex(num)

		if err := ValidateOperatorSubstitution(ctxN, sub, linkForImage); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	return validationErrors
}

func ValidateOperatorSubstitution(ctx *configContext, sub api.PullSpecSubstitution, linkForImage func(string) api.StepLink) error {
	if sub.PullSpec == "" {
		return ctx.AddField("pullspec").errorf("must be set")
	}
	if sub.With == "" {
		return ctx.AddField("with").errorf("must be set")
	}

	if link := linkForImage(sub.With); link == nil {
		return ctx.AddField("with").errorf("could not resolve '%s' to an image involved in the config", sub.With)
	}

	return nil
}

func ValidateBaseImages(ctx *configContext, images map[string]api.ImageStreamTagReference) []error {
	ret := validateImageStreamTagReferenceMap("base_images", images)
	for name := range images {
		if err := ctx.addKey(name).addPipelineImage(api.PipelineImageStreamTagReference(name)); err != nil {
			ret = append(ret, err)
		}
	}
	return ret
}

func validateBaseRPMImages(ctx *configContext, images map[string]api.ImageStreamTagReference) []error {
	ret := validateImageStreamTagReferenceMap("base_rpm_images", images)
	for name := range images {
		ctxN := ctx.addKey(name)
		if err := ctxN.addPipelineImage(api.PipelineImageStreamTagReference(fmt.Sprintf("%s-without-rpms", name))); err != nil {
			ret = append(ret, err)
		}
		if err := ctxN.addPipelineImage(api.PipelineImageStreamTagReference(name)); err != nil {
			ret = append(ret, err)
		}
	}
	return ret
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

	// Third conjunct is a corner case, the config can e.g. promote its `src`
	if len(input.Tests) == 0 && len(input.Images) == 0 && (input.PromotionConfiguration == nil || len(input.PromotionConfiguration.AdditionalImages) == 0) {
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
