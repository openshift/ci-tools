package promotion

import (
	cioperatorapi "github.com/openshift/ci-operator/pkg/api"
)

const (
	okdPromotionNamespace = "openshift"
	okd40Imagestream      = "origin-v4.0"
	ocpPromotionNamespace = "ocp"
)

// PromotesOfficialImages determines if a configuration will result in official images
// being promoted. This is a proxy for determining if a configuration contributes to
// the release payload.
func PromotesOfficialImages(configSpec *cioperatorapi.ReleaseBuildConfiguration) bool {
	promotionNamespace := extractPromotionNamespace(configSpec)
	promotionName := extractPromotionName(configSpec)
	return (promotionNamespace == okdPromotionNamespace && promotionName == okd40Imagestream) || promotionNamespace == ocpPromotionNamespace
}

func extractPromotionNamespace(configSpec *cioperatorapi.ReleaseBuildConfiguration) string {
	if configSpec.PromotionConfiguration != nil && configSpec.PromotionConfiguration.Namespace != "" {
		return configSpec.PromotionConfiguration.Namespace
	}

	return ""
}

func extractPromotionName(configSpec *cioperatorapi.ReleaseBuildConfiguration) string {
	if configSpec.PromotionConfiguration != nil && configSpec.PromotionConfiguration.Name != "" {
		return configSpec.PromotionConfiguration.Name
	}

	return ""
}
