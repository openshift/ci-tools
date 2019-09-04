package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/diffs"
	"github.com/openshift/ci-tools/pkg/promotion"
)

type options struct {
	currentRelease      string
	releaseRepoDir      string
	ocpBuildDataRepoDir string

	logLevel string
}

func (o *options) Validate() error {
	if o.releaseRepoDir == "" {
		return errors.New("required flag --release-repo-dir was unset")
	}

	if o.ocpBuildDataRepoDir == "" {
		return errors.New("required flag --ocp-build-data-repo-dir was unset")
	}

	if o.currentRelease == "" {
		return errors.New("required flag --current-release was unset")
	}

	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %v", err)
	}
	logrus.SetLevel(level)
	return nil
}

func (o *options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.currentRelease, "current-release", "", "Configurations targeting this release will get validated.")
	fs.StringVar(&o.releaseRepoDir, "release-repo-dir", "", "Path to openshift/release repo.")
	fs.StringVar(&o.ocpBuildDataRepoDir, "ocp-build-data-repo-dir", "", "Path to openshift/ocp-build-data repo.")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	raw, err := ioutil.ReadFile(filepath.Join(o.ocpBuildDataRepoDir, "group.yml"))
	if err != nil {
		logrus.WithError(err).Fatal("Could not load OCP build data branch configuration.")
	}

	var groupConfig branchConfig
	if err := yaml.Unmarshal(raw, &groupConfig); err != nil {
		logrus.WithError(err).Fatal("Could not unmarshal OCP build data branch configuration.")
	}
	targetRelease := fmt.Sprintf("%d.%d", groupConfig.Vars.Major, groupConfig.Vars.Minor)
	if expected, actual := targetRelease, o.currentRelease; expected != actual {
		logrus.Fatalf("Release configured in OCP build data (%s) does not match that in CI (%s)", expected, actual)
	}

	imageConfigByName := map[string]imageConfig{}
	if err := filepath.Walk(filepath.Join(o.ocpBuildDataRepoDir, "images"), func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}

		// we know the path is relative, but there is no API to declare that
		relPath, _ := filepath.Rel(o.ocpBuildDataRepoDir, path)
		logger := logrus.WithField("source-file", relPath)
		raw, err := ioutil.ReadFile(path)
		if err != nil {
			logger.WithError(err).Fatal("Could not load OCP build data configuration.")
		}

		var productConfig imageConfig
		if err := yaml.Unmarshal(raw, &productConfig); err != nil {
			logger.WithError(err).Fatal("Could not unmarshal OCP build data configuration.")
		}
		productConfig.path = relPath

		imageConfigByName[productConfig.Name] = productConfig
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could walk OCP build data configuration directory.")
	}

	var foundFailures bool
	if err := config.OperateOnCIOperatorConfigDir(path.Join(o.releaseRepoDir, diffs.CIOperatorConfigInRepoPath), func(configuration *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
		if !(promotion.PromotesOfficialImages(configuration) && configuration.PromotionConfiguration.Name == o.currentRelease) {
			return nil
		}
		logger := config.LoggerForInfo(*info)

		for _, image := range configuration.Images {
			if image.Optional {
				continue
			}
			logger = logger.WithField("image", image.To)
			productImageName := fmt.Sprintf("openshift/ose-%s", image.To)
			logger.Debug("Validating image.")
			productConfig, exists := imageConfigByName[productImageName]
			if !exists {
				logger.Errorf("Promotion found in CI for image %s, but no configuration for %s found in OCP build data.", image.To, productImageName)
				continue
			}
			logger = logger.WithField("ocp-build-data-path", productConfig.path)

			resolvedBranch := strings.Replace(productConfig.Content.Source.Git.Branch.Target, "{MAJOR}.{MINOR}", targetRelease, -1)
			if actual, expected := info.Branch, resolvedBranch; actual != expected {
				if expected == "" {
					logger.Error("Target branch not set in OCP build data configuration.")
				} else {
					logger.Errorf("Target branch in CI Operator configuration (%s) does not match that resolved from OCP build data (%s).", actual, expected)
				}
				foundFailures = true
			}

			// there is no standard, we just need to generally point at the right thing
			urls := []string{
				fmt.Sprintf("git@github.com:%s/%s", info.Org, info.Repo),
				fmt.Sprintf("git@github.com:%s/%s.git", info.Org, info.Repo),
				fmt.Sprintf("https://github.com/%s/%s", info.Org, info.Repo),
				fmt.Sprintf("https://github.com/%s/%s.git", info.Org, info.Repo),
			}
			if actual, expected := productConfig.Content.Source.Git.Url, sets.NewString(urls...); !expected.Has(actual) {
				if actual == "" {
					logger.Error("Source repo URL not set in OCP build data configuration.")
				} else {
					logger.Errorf("Source repo URL in OCP build data (%s) is not a recognized URL for %s/%s.", actual, info.Org, info.Repo)
				}
				foundFailures = true
			}
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not load CI Operator configurations.")
	}

	if foundFailures {
		logrus.Fatal("Found configurations that promote to official streams but do not have corresponding OCP build data configurations.")
	}
}

// branchConfig holds branch-wide configurations in the ocp-build-data repository
type branchConfig struct {
	Vars vars `yaml:"vars"`
}

type vars struct {
	Major int `yaml:"MAJOR"`
	Minor int `yaml:"MINOR"`
}

// imageConfig is the configuration stored in the ocp-build-data repository
type imageConfig struct {
	Content content `yaml:"content"`
	Name    string  `yaml:"name"`

	// added by us
	path string
}

type content struct {
	Source source `yaml:"source"`
}

type source struct {
	Git git `yaml:"git"`
}

type git struct {
	Branch branch `yaml:"branch"`
	Url    string `yaml:"url"`
}

type branch struct {
	Target string `yaml:"target,omitempty"`
}
