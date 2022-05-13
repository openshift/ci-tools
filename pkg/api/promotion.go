package api

type OKDInclusion bool

const (
	okdPromotionNamespace = "origin"
	ocpPromotionNamespace = "ocp"

	WithOKD    OKDInclusion = true
	WithoutOKD OKDInclusion = false
)

// PromotesOfficialImages determines if a configuration will result in official images
// being promoted. This is a proxy for determining if a configuration contributes to
// the release payload.
func PromotesOfficialImages(configSpec *ReleaseBuildConfiguration, includeOKD OKDInclusion) bool {
	return !IsPromotionDisabled(configSpec) && BuildsOfficialImages(configSpec, includeOKD)
}

// IsPromotionDisabled determines if promotion is disabled in the configuration
func IsPromotionDisabled(configSpec *ReleaseBuildConfiguration) bool {
	return configSpec.PromotionConfiguration != nil && configSpec.PromotionConfiguration.Disabled
}

// BuildsOfficialImages determines if a configuration will result in official images
// being built.
func BuildsOfficialImages(configSpec *ReleaseBuildConfiguration, includeOKD OKDInclusion) bool {
	promotionNamespace := ExtractPromotionNamespace(configSpec)
	return RefersToOfficialImage(promotionNamespace, includeOKD)
}

// RefersToOfficialImage determines if an image is official
func RefersToOfficialImage(namespace string, includeOKD OKDInclusion) bool {
	return (bool(includeOKD) && namespace == okdPromotionNamespace) || namespace == ocpPromotionNamespace
}

// ExtractPromotionNamespace extracts the promotion namespace
func ExtractPromotionNamespace(configSpec *ReleaseBuildConfiguration) string {
	if configSpec.PromotionConfiguration != nil && configSpec.PromotionConfiguration.Namespace != "" {
		return configSpec.PromotionConfiguration.Namespace
	}
	return ""
}

// ExtractPromotionName extracts the promotion name
func ExtractPromotionName(configSpec *ReleaseBuildConfiguration) string {
	if configSpec.PromotionConfiguration != nil && configSpec.PromotionConfiguration.Name != "" {
		return configSpec.PromotionConfiguration.Name
	}
	return ""
}
