package promotion

import (
	"errors"
	"flag"
	"fmt"
	"regexp"

	"github.com/sirupsen/logrus"

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
	return !isDisabled(configSpec) && BuildsOfficialImages(configSpec)
}

func isDisabled(configSpec *cioperatorapi.ReleaseBuildConfiguration) bool {
	return configSpec.PromotionConfiguration != nil && configSpec.PromotionConfiguration.Disabled
}

// BuildsOfficialImages determines if a configuration will result in official images
// being built.
func BuildsOfficialImages(configSpec *cioperatorapi.ReleaseBuildConfiguration) bool {
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

// DetermineReleaseBranches determines the branch that will be used to continue promoting
// to the current release as well as the branch that will promote to the future release,
// based on the branch that is currently promoting to the current release.
func DetermineReleaseBranches(currentRelease, futureRelease, currentBranch string) (string, string, error) {
	// futureBranchForCurrentRelease is the branch that will promote to the current imagestream once we branch configs
	var futureBranchForCurrentRelease string
	// futureBranchForFutureRelease is the branch that will promote to the future imagestream once we branch configs
	var futureBranchForFutureRelease string
	if currentBranch == "master" {
		futureBranchForCurrentRelease = fmt.Sprintf("release-%s", currentRelease)
		futureBranchForFutureRelease = currentBranch
	} else if currentBranch == fmt.Sprintf("openshift-%s", currentRelease) {
		futureBranchForCurrentRelease = currentBranch
		futureBranchForFutureRelease = fmt.Sprintf("openshift-%s", futureRelease)
	} else {
		return "", "", fmt.Errorf("invalid branch %q promoting to current release", currentBranch)
	}
	return futureBranchForCurrentRelease, futureBranchForFutureRelease, nil
}

type Options struct {
	ConfigDir      string
	CurrentRelease string
	FutureRelease  string
	Mirror         bool
	Unmirror       bool
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

	if o.FutureRelease == "" {
		return errors.New("required flag --future-release was unset")
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
	fs.StringVar(&o.FutureRelease, "future-release", "", "Configurations will get branched to target this release.")
	fs.BoolVar(&o.Mirror, "mirror", false, "Mirror the promotion config, but disable it in the target.")
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
