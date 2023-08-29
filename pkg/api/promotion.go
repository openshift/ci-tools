package api

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
)

type OKDInclusion bool

const (
	okdPromotionNamespace = "origin"
	ocpPromotionNamespace = "ocp"

	WithOKD    OKDInclusion = true
	WithoutOKD OKDInclusion = false

	PromotionStepName     = "promotion"
	PromotionQuayStepName = "promotion-quay"
)

// ImageTargets returns image targets for the main promotion step (which is opt-out)
func ImageTargets(c *ReleaseBuildConfiguration) sets.Set[string] {
	imageTargets := sets.New[string]()
	if c.PromotionConfiguration != nil {
		for additional := range c.PromotionConfiguration.AdditionalImages {
			imageTargets.Insert(c.PromotionConfiguration.AdditionalImages[additional])
		}
	}

	if len(c.Images) > 0 || imageTargets.Len() > 0 {
		imageTargets.Insert("[images]")
	}
	return imageTargets
}

// AccessoryImageTargets returns image targets for an accessory promotion step (which are opt-in)
func AccessoryImageTargets(c *PromotionConfiguration) sets.Set[string] {
	imageTargets := sets.New[string]()
	if c != nil {
		for image := range c.AdditionalImages {
			imageTargets.Insert(c.AdditionalImages[image])
		}
	}

	return imageTargets
}

// PromotesOfficialImages determines if a configuration will result in official images
// being promoted. This is a proxy for determining if a configuration contributes to
// the release payload.
func PromotesOfficialImages(configSpec *PromotionConfiguration, includeOKD OKDInclusion) bool {
	return !IsPromotionDisabled(configSpec) && BuildsOfficialImages(configSpec, includeOKD)
}

// IsPromotionDisabled determines if promotion is disabled in the configuration
func IsPromotionDisabled(configSpec *PromotionConfiguration) bool {
	return configSpec != nil && configSpec.Disabled
}

// BuildsOfficialImages determines if a configuration will result in official images
// being built.
func BuildsOfficialImages(configSpec *PromotionConfiguration, includeOKD OKDInclusion) bool {
	promotionNamespace := ExtractPromotionNamespace(configSpec)
	return RefersToOfficialImage(promotionNamespace, includeOKD)
}

// RefersToOfficialImage determines if an image is official
func RefersToOfficialImage(namespace string, includeOKD OKDInclusion) bool {
	return (bool(includeOKD) && namespace == okdPromotionNamespace) || namespace == ocpPromotionNamespace
}

// ExtractPromotionNamespace extracts the promotion namespace
func ExtractPromotionNamespace(configSpec *PromotionConfiguration) string {
	if configSpec != nil && configSpec.Namespace != "" {
		return configSpec.Namespace
	}
	return ""
}

// ExtractPromotionName extracts the promotion name
func ExtractPromotionName(configSpec *PromotionConfiguration) string {
	if configSpec != nil && configSpec.Name != "" {
		return configSpec.Name
	}
	return ""
}

func tagsInQuay(image string, tag ImageStreamTagReference, date string) ([]string, error) {
	if date == "" {
		return nil, fmt.Errorf("date must not be empty")
	}
	splits := strings.Split(image, "@sha256:")
	if len(splits) != 2 {
		return nil, fmt.Errorf("malformed image pull spec: %s", image)
	}
	digest := splits[1]
	return []string{
		fmt.Sprintf("%s:%s_sha256_%s", QuayOpenShiftCIRepo, date, digest),
		fmt.Sprintf("%s:%s_%s_%s", QuayOpenShiftCIRepo, tag.Namespace, tag.Name, tag.Tag),
	}, nil
}

var (
	// DefaultMirrorFunc is the default mirroring function
	DefaultMirrorFunc = func(source, target string, _ ImageStreamTagReference, _ string, mirror map[string]string) {
		mirror[target] = source
	}
	// QuayMirrorFunc is the mirroring function for quay.io
	QuayMirrorFunc = func(source, target string, tag ImageStreamTagReference, date string, mirror map[string]string) {
		if quayTags, err := tagsInQuay(source, tag, date); err != nil {
			logrus.WithField("source", source).WithError(err).
				Warn("Failed to get the tag in quay.io and skipped the promotion to quay for this image")
		} else {
			for _, quayTag := range quayTags {
				mirror[quayTag] = source
			}
		}
	}

	// DefaultTargetNameFunc is the default target name function
	DefaultTargetNameFunc = func(registry string, config PromotionConfiguration) string {
		if len(config.Name) > 0 {
			return fmt.Sprintf("%s/%s/%s:${component}", registry, config.Namespace, config.Name)
		}
		return fmt.Sprintf("%s/%s/${component}:%s", registry, config.Namespace, config.Tag)
	}

	// QuayTargetNameFunc is the target name function for quay.io
	QuayTargetNameFunc = func(_ string, config PromotionConfiguration) string {
		if len(config.Name) > 0 {
			return fmt.Sprintf("%s:%s_%s_${component}", QuayOpenShiftCIRepo, config.Namespace, config.Name)
		}
		return fmt.Sprintf("%s:%s_${component}_%s", QuayOpenShiftCIRepo, config.Namespace, config.Tag)
	}
)
