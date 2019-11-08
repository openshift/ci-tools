package promotion

import (
	"errors"
	"flag"
	"fmt"
	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"
	"regexp"
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
	return !isDisabled(configSpec) && buildOfficialImages(configSpec)
}

func isDisabled(configSpec *cioperatorapi.ReleaseBuildConfiguration) bool {
	return configSpec.PromotionConfiguration != nil && configSpec.PromotionConfiguration.Disabled
}

// buildOfficialImages determines if a configuration will result in official images
// being built.
func buildOfficialImages(configSpec *cioperatorapi.ReleaseBuildConfiguration) bool {
	promotionNamespace := extractPromotionNamespace(configSpec)
	promotionName := extractPromotionName(configSpec)
	return RefersToOfficialImage(promotionName, promotionNamespace)
}

// RefersToOfficialImage determines if an image is official
func RefersToOfficialImage(name, namespace string) bool {
	return (namespace == okdPromotionNamespace && name == okd40Imagestream) || namespace == ocpPromotionNamespace
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

// IsBumpable determines if the dev branch should be bumped or not
func IsBumpable(branch, currentRelease string) bool {
	return branch != fmt.Sprintf("openshift-%s", currentRelease)
}

// DetermineReleaseBranch determines the branch that will be used to the future release,
// based on the branch that is currently promoting to the current release.
func DetermineReleaseBranch(currentRelease, futureRelease, currentBranch string) (string, error) {
	if currentBranch == "master" {
		return fmt.Sprintf("release-%s", futureRelease), nil
	} else if currentBranch == fmt.Sprintf("openshift-%s", currentRelease) {
		return fmt.Sprintf("openshift-%s", futureRelease), nil
	} else if currentBranch == fmt.Sprintf("release-%s", currentRelease) {
		return fmt.Sprintf("release-%s", futureRelease), nil
	} else {
		return "", fmt.Errorf("invalid branch %q promoting to current release", currentBranch)
	}
}

type Options struct {
	ConfigDir      string
	CurrentRelease string
	FutureReleases flagutil.Strings
	BumpRelease    string
	Confirm        bool
	Org            string
	Repo           string

	logLevel string
}

func (o *Options) Validate() error {
	if o.ConfigDir == "" {
		return errors.New("required flag --config-dir was unset")
	}

	if o.CurrentRelease == "" {
		return errors.New("required flag --current-release was unset")
	}

	if len(o.FutureReleases.Strings()) == 0 {
		return errors.New("required flag --future-release was not provided at least once")
	}

	// we always want to make sure that we are updating the config for the release
	// branch that tracks the current release, but we don't need the user to provide
	// the value twice in flags
	if err := o.FutureReleases.Set(o.CurrentRelease); err != nil {
		return fmt.Errorf("could not add current release to future releases: %v", err)
	}

	futureReleases := sets.NewString(o.FutureReleases.Strings()...)
	if o.BumpRelease != "" && !futureReleases.Has(o.BumpRelease) {
		return fmt.Errorf("future releases %v do not contain bump release %v", futureReleases.List(), o.BumpRelease)
	}

	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %v", err)
	}
	logrus.SetLevel(level)
	return nil
}

func (o *Options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.ConfigDir, "config-dir", "", "Path to CI Operator configuration directory.")
	fs.StringVar(&o.CurrentRelease, "current-release", "", "Configurations targeting this release will get branched.")
	fs.Var(&o.FutureReleases, "future-release", "Configurations will get branched to target this release, provide one or more times.")
	fs.StringVar(&o.BumpRelease, "bump-release", "", "Bump the dev config to this release and manage mirroring.")
	fs.BoolVar(&o.Confirm, "confirm", false, "Create the branched configuration files.")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.Org, "org", "", "Limit repos affected to those in this org.")
	fs.StringVar(&o.Repo, "repo", "", "Limit repos affected to this repo.")
}

var threeXBranches = regexp.MustCompile(`^(release|enterprise|openshift)-3\.[0-9]+$`)
var fourXBranches = regexp.MustCompile(`^(release|enterprise|openshift)-(4\.[0-9]+)$`)

func FlavorForBranch(branch string) string {
	var flavor string
	if branch == "master" {
		flavor = "master"
	} else if threeXBranches.MatchString(branch) {
		flavor = "3.x"
	} else if fourXBranches.MatchString(branch) {
		matches := fourXBranches.FindStringSubmatch(branch)
		flavor = matches[2] // the 4.x release string
	} else {
		flavor = "misc"
	}
	return flavor
}
