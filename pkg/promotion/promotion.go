package promotion

import (
	"errors"
	"flag"
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

// PromotesImagesInto determines if a configuration will result in images being promoted.
func PromotesImagesInto(configSpec *cioperatorapi.ReleaseBuildConfiguration, promotionNamespace string) bool {
	if promotionNamespace == "" {
		return false
	}
	if !cioperatorapi.IsPromotionDisabled(configSpec.PromotionConfiguration) && promotionNamespace == cioperatorapi.ExtractPromotionNamespace(configSpec.PromotionConfiguration) {
		return true
	}
	for _, cfg := range configSpec.AccessoryPromotionConfiguration {
		if !cioperatorapi.IsPromotionDisabled(&cfg) && promotionNamespace == cioperatorapi.ExtractPromotionNamespace(&cfg) {
			return true
		}
	}
	return false
}

// AllPromotionImageStreamTags returns a set of all ImageStreamTags this config promotes to.
func AllPromotionImageStreamTags(configSpec *cioperatorapi.ReleaseBuildConfiguration) sets.Set[string] {
	result := sets.Set[string]{}

	promotionConfigs := []*cioperatorapi.PromotionConfiguration{
		configSpec.PromotionConfiguration,
	}
	for _, cfg := range configSpec.AccessoryPromotionConfiguration {
		promotionConfigs = append(promotionConfigs, &cfg)
	}

	for _, cfg := range promotionConfigs {
		if cioperatorapi.IsPromotionDisabled(cfg) {
			return result
		}

		namespace := cioperatorapi.ExtractPromotionNamespace(cfg)
		name := cioperatorapi.ExtractPromotionName(cfg)

		if namespace == "" || name == "" {
			return result
		}

		for _, image := range configSpec.Images {
			result.Insert(fmt.Sprintf("%s/%s:%s", namespace, name, image.To))
		}

		for additionalTagToPromote := range cfg.AdditionalImages {
			result.Insert(fmt.Sprintf("%s/%s:%s", namespace, name, additionalTagToPromote))
		}
	}

	return result
}

// IsBumpable determines if the dev branch should be bumped or not
func IsBumpable(branch, currentRelease string) bool {
	return branch != fmt.Sprintf("openshift-%s", currentRelease)
}

// DetermineReleaseBranch determines the branch that will be used to the future release,
// based on the branch that is currently promoting to the current release.
func DetermineReleaseBranch(currentRelease, futureRelease, currentBranch string) (string, error) {
	switch currentBranch {
	case "master", "main":
		return fmt.Sprintf("release-%s", futureRelease), nil
	case fmt.Sprintf("openshift-%s", currentRelease):
		return fmt.Sprintf("openshift-%s", futureRelease), nil
	case fmt.Sprintf("release-%s", currentRelease):
		return fmt.Sprintf("release-%s", futureRelease), nil
	default:
		return "", fmt.Errorf("invalid branch %q promoting to current release", currentBranch)
	}
}

// FutureOptions holds options to load CI Operator configuration and
// operate on a subset of them to update for future releases.
type FutureOptions struct {
	Options

	FutureReleases flagutil.Strings
}

func (o *FutureOptions) Validate() error {
	if len(o.FutureReleases.Strings()) == 0 {
		return errors.New("required flag --future-release was not provided at least once")
	}

	// we always want to make sure that we are updating the config for the release
	// branch that tracks the current release, but we don't need the user to provide
	// the value twice in flags
	if err := o.FutureReleases.Set(o.CurrentRelease); err != nil {
		return fmt.Errorf("could not add current release to future releases: %w", err)
	}

	return o.Options.Validate()
}

func (o *FutureOptions) Bind(fs *flag.FlagSet) {
	fs.Var(&o.FutureReleases, "future-release", "Configurations will get branched to target this release, provide one or more times.")
	o.Options.Bind(fs)
}

// Options holds options to load CI Operator configuration
// and select a subset of that configuration to operate on.
// Configurations can be filtered by current release.
type Options struct {
	config.ConfirmableOptions

	CurrentRelease            string
	CurrentPromotionNamespace string
}

func (o *Options) Validate() error {
	if o.CurrentRelease == "" {
		return errors.New("required flag --current-release was unset")
	}

	return o.ConfirmableOptions.Validate()
}

func (o *Options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.CurrentRelease, "current-release", "", "Configurations targeting this release will get branched.")
	fs.StringVar(&o.CurrentPromotionNamespace, "current-promotion-namespace", "", "The promotion namespace of the current release.")
	o.ConfirmableOptions.Bind(fs)
}

func (o *Options) matches(configuration *cioperatorapi.ReleaseBuildConfiguration, includeOKD cioperatorapi.OKDInclusion) bool {
	var imagesMatch bool
	if o.CurrentPromotionNamespace == "" {
		imagesMatch = cioperatorapi.PromotesOfficialImages(configuration.PromotionConfiguration, includeOKD)
	} else {
		imagesMatch = PromotesImagesInto(configuration, o.CurrentPromotionNamespace)
	}
	return imagesMatch && configuration.PromotionConfiguration.Name == o.CurrentRelease
}

// OperateOnCIOperatorConfigDir filters the full set of configurations
// down to those that were selected by the user with promotion options
func (o *Options) OperateOnCIOperatorConfigDir(configDir string, includeOKD cioperatorapi.OKDInclusion, callback func(*cioperatorapi.ReleaseBuildConfiguration, *config.Info) error) error {
	return o.Options.OperateOnCIOperatorConfigDir(configDir, func(configuration *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
		if !o.matches(configuration, includeOKD) {
			return nil
		}
		return callback(configuration, info)
	})
}
