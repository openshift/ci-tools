package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	cioperatorapi "github.com/openshift/ci-operator/pkg/api"
)

func readCiOperatorConfig(configFilePath string) (*cioperatorapi.ReleaseBuildConfiguration, error) {
	data, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read ci-operator config (%v)", err)
	}

	var configSpec *cioperatorapi.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(data, &configSpec); err != nil {
		return nil, fmt.Errorf("failed to load ci-operator config (%v)", err)
	}

	if err := configSpec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid ci-operator config: %v", err)
	}

	return configSpec, nil
}

// path to ci-operator configuration file encodes information about tested code
// .../$ORGANIZATION/$REPOSITORY/$ORGANIZATION-$COMPONENT-$BRANCH.$EXT
type FilePathElements struct {
	Org      string
	Repo     string
	Branch   string
	Variant  string
	Filename string
}

// We use the directory/file naming convention to encode useful information
// about component repository information.
// The convention for ci-operator config files in this repo:
// ci-operator/config/ORGANIZATION/COMPONENT/ORGANIZATION-COMPONENT-BRANCH.yaml
func extractRepoElementsFromPath(configFilePath string) (*FilePathElements, error) {
	configSpecDir := filepath.Dir(configFilePath)
	repo := filepath.Base(configSpecDir)
	if repo == "." || repo == "/" {
		return nil, fmt.Errorf("could not extract repo from '%s' (expected path like '.../ORG/REPO/ORGANIZATION-COMPONENT-BRANCH.yaml", configFilePath)
	}

	org := filepath.Base(filepath.Dir(configSpecDir))
	if org == "." || org == "/" {
		return nil, fmt.Errorf("could not extract org from '%s' (expected path like '.../ORG/REPO/ORGANIZATION-COMPONENT-BRANCH.yaml", configFilePath)
	}

	fileName := filepath.Base(configFilePath)
	s := strings.TrimSuffix(fileName, filepath.Ext(configFilePath))
	branch := strings.TrimPrefix(s, fmt.Sprintf("%s-%s-", org, repo))

	var variant string
	if i := strings.LastIndex(branch, "__"); i != -1 {
		variant = branch[i+2:]
		branch = branch[:i]
	}

	return &FilePathElements{org, repo, branch, variant, fileName}, nil
}

func isConfigFile(path string, info os.FileInfo) bool {
	extension := filepath.Ext(path)
	return !info.IsDir() && (extension == ".yaml" || extension == ".yml")
}

// OperateOnCIOperatorConfig runs the callback on the parsed data from
// the CI Operator configuration file provided
func OperateOnCIOperatorConfig(path string, callback func(*cioperatorapi.ReleaseBuildConfiguration, *FilePathElements) error) error {
	jobConfig, err := readCiOperatorConfig(path)
	if err != nil {
		logrus.WithField("source-file", path).WithError(err).Error("Failed to load CI Operator configuration")
		return err
	}

	repoInfo, err := extractRepoElementsFromPath(path)
	if err != nil {
		logrus.WithField("source-file", path).WithError(err).Error("Failed to load CI Operator configuration")
		return err
	}
	if err = callback(jobConfig, repoInfo); err != nil {
		logrus.WithField("source-file", path).WithError(err).Error("Failed to execute callback")
		return err
	}
	return nil
}

// OperateOnCIOperatorConfigDir runs the callback on all CI Operator
// configuration files found while walking the directory provided
func OperateOnCIOperatorConfigDir(configDir string, callback func(*cioperatorapi.ReleaseBuildConfiguration, *FilePathElements) error) error {
	return filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logrus.WithField("source-file", path).WithError(err).Error("Failed to walk CI Operator configuration dir")
			return err
		}
		if isConfigFile(path, info) {
			if err := OperateOnCIOperatorConfig(path, callback); err != nil {
				return err
			}
		}
		return nil
	})
}

func LoggerForInfo(repoInfo FilePathElements) *logrus.Entry {
	return logrus.WithFields(logrus.Fields{
		"org":         repoInfo.Org,
		"repo":        repoInfo.Repo,
		"branch":      repoInfo.Branch,
		"source-file": repoInfo.Filename,
	})
}

type CompoundCiopConfig map[string]*cioperatorapi.ReleaseBuildConfiguration

func (compound CompoundCiopConfig) add(handledConfig *cioperatorapi.ReleaseBuildConfiguration, handledElements *FilePathElements) error {
	compound[handledElements.Filename] = handledConfig
	return nil
}

func CompoundLoad(path string) (CompoundCiopConfig, error) {
	config := CompoundCiopConfig{}
	if err := OperateOnCIOperatorConfigDir(path, config.add); err != nil {
		return nil, err
	}

	return config, nil
}

