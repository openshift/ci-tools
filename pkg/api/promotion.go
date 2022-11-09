package api

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"

	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
)

type OKDInclusion bool

const (
	okdPromotionNamespace = "origin"
	ocpPromotionNamespace = "ocp"

	WithOKD    OKDInclusion = true
	WithoutOKD OKDInclusion = false
)

// ImageTargets returns image targets
func ImageTargets(c *ReleaseBuildConfiguration) sets.String {
	imageTargets := sets.NewString()
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

// GetGitTags returns the git tags that point to the commit for a GitHub repo/org
func GetGitTags(org, repo, commit string) ([]string, error) {
	if org == "" {
		return nil, errors.New("org must not be empty")
	}
	if repo == "" {
		return nil, errors.New("repo must not be empty")
	}
	if commit == "" {
		return nil, errors.New("commit must not be empty")
	}

	response, err := http.Get(fmt.Sprintf("https://api.github.com/repos/%s/%s/tags", org, repo))
	if err != nil {
		return nil, fmt.Errorf("failed to get git tags via GitHub's API: %w", err)
	}
	bytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response's body: %w", err)
	}
	var gitTags []GitTag
	if err := json.Unmarshal(bytes, &gitTags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal git tags: %w", err)
	}
	var ret []string
	for _, gitTag := range gitTags {
		if gitTag.Commit.Sha == commit {
			ret = append(ret, gitTag.Name)
		}
	}
	return ret, nil
}

type GitTag struct {
	Name   string `json:"name,omitempty"`
	Commit Commit `json:"commit"`
}

type Commit struct {
	Sha string `json:"sha,omitempty"`
}
