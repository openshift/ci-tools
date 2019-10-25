package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/openshift/ci-tools/pkg/promotion"
	"github.com/sirupsen/logrus"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

const (
	prowgenConfigFile = ".config.prowgen"
)

func readCiOperatorConfig(configFilePath string, info Info) (*cioperatorapi.ReleaseBuildConfiguration, error) {
	data, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read ci-operator config (%v)", err)
	}

	var configSpec *cioperatorapi.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(data, &configSpec); err != nil {
		return nil, fmt.Errorf("failed to load ci-operator config (%v)", err)
	}

	if err := configSpec.Validate(info.Org, info.Repo); err != nil {
		return nil, fmt.Errorf("invalid ci-operator config: %v", err)
	}

	return configSpec, nil
}

var prowgenConfigs map[string]*Prowgen

// Prowgen holds the information of the prowgen's configuration file.
type Prowgen struct {
	Private bool `json:"private,omitempty"`
}

// DataWithInfo describes the metadata for a CI Operator configuration file
type Info struct {
	Org    string
	Repo   string
	Branch string
	// Variant allows for parallel configuration files for one (org,repo,branch)
	Variant string
	// Filename is the full path to the file on disk
	Filename string

	ProwgenConfig Prowgen
}

// Basename returns the unique name for this file in the config
func (i *Info) Basename() string {
	basename := strings.Join([]string{i.Org, i.Repo, i.Branch}, "-")
	if i.Variant != "" {
		basename = fmt.Sprintf("%s__%s", basename, i.Variant)
	}
	return fmt.Sprintf("%s.yaml", basename)
}

// ConfigMapName returns the configmap in which we expect this file to be uploaded
func (i *Info) ConfigMapName() string {
	return fmt.Sprintf("ci-operator-%s-configs", promotion.FlavorForBranch(i.Branch))
}

// IsCiopConfigCM returns true if a given name is a valid ci-operator config ConfigMap
func IsCiopConfigCM(name string) bool {
	return regexp.MustCompile(`^ci-operator-.+-configs$`).MatchString(name)
}

func readProwgenConfig(path string) (*Prowgen, error) {
	var pConfig *Prowgen
	b, err := ioutil.ReadFile(filepath.Join(path, prowgenConfigFile))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("prowgen config found in path %s but couldn't read the file: %v", path, err)
	}

	if err == nil {
		if err := yaml.Unmarshal(b, &pConfig); err != nil {
			return nil, fmt.Errorf("prowgen config found in path %sbut couldn't unmarshal it: %v", path, err)
		}
	}

	return pConfig, nil
}

// We use the directory/file naming convention to encode useful information
// about component repository information.
// The convention for ci-operator config files in this repo:
// ci-operator/config/ORGANIZATION/COMPONENT/ORGANIZATION-COMPONENT-BRANCH.yaml
func InfoFromPath(configFilePath string) (*Info, error) {
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

	pConfig := Prowgen{}
	if pc, ok := prowgenConfigs[fmt.Sprintf("%s/%s", org, repo)]; ok {
		pConfig = *pc
	} else if pc, ok := prowgenConfigs[org]; ok {
		pConfig = *pc
	}

	return &Info{
		Org:           org,
		Repo:          repo,
		Branch:        branch,
		Variant:       variant,
		Filename:      configFilePath,
		ProwgenConfig: pConfig,
	}, nil
}

func isConfigFile(path string, info os.FileInfo) bool {
	extension := filepath.Ext(path)
	return !info.IsDir() && (extension == ".yaml" || extension == ".yml")
}

// OperateOnCIOperatorConfig runs the callback on the parsed data from
// the CI Operator configuration file provided
func OperateOnCIOperatorConfig(path string, callback func(*cioperatorapi.ReleaseBuildConfiguration, *Info) error) error {
	info, err := InfoFromPath(path)
	if err != nil {
		logrus.WithField("source-file", path).WithError(err).Error("Failed to resolve info from CI Operator configuration path")
		return err
	}
	jobConfig, err := readCiOperatorConfig(path, *info)
	if err != nil {
		logrus.WithField("source-file", path).WithError(err).Error("Failed to load CI Operator configuration")
		return err
	}
	if err = callback(jobConfig, info); err != nil {
		logrus.WithField("source-file", path).WithError(err).Error("Failed to execute callback")
		return err
	}
	return nil
}

// OperateOnCIOperatorConfigDir runs the callback on all CI Operator
// configuration files found while walking the directory provided
func OperateOnCIOperatorConfigDir(configDir string, callback func(*cioperatorapi.ReleaseBuildConfiguration, *Info) error) error {
	return OperateOnCIOperatorConfigSubdir(configDir, "", callback)
}

func OperateOnCIOperatorConfigSubdir(configDir, subDir string, callback func(*cioperatorapi.ReleaseBuildConfiguration, *Info) error) error {
	return filepath.Walk(filepath.Join(configDir, subDir), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logrus.WithField("source-file", path).WithError(err).Error("Failed to walk CI Operator configuration dir")
			return err
		}

		if info.IsDir() {
			pConfig, err := readProwgenConfig(path)
			if err != nil {
				return err
			}

			if len(prowgenConfigs) == 0 {
				prowgenConfigs = make(map[string]*Prowgen)
			}

			// Trim the --from-dir value from the current path. This will leave us with
			// a org/repo value. Example `ci-operator/config/openshift/release` will be
			// converted to `openshift/release` and `ci-operator/config/openshift` to `openshift`
			orgRepo := strings.TrimPrefix(path, fmt.Sprintf("%s/", filepath.Clean(configDir)))
			if len(orgRepo) > 0 && pConfig != nil {
				prowgenConfigs[orgRepo] = pConfig
			}
		}

		if isConfigFile(path, info) {
			if err := OperateOnCIOperatorConfig(path, callback); err != nil {
				return err
			}
		}
		return nil
	})
}

func LoggerForInfo(info Info) *logrus.Entry {
	return logrus.WithFields(logrus.Fields{
		"org":         info.Org,
		"repo":        info.Repo,
		"branch":      info.Branch,
		"source-file": info.Basename(),
	})
}

// DataWithInfo wraps a CI Operator configuration and metadata for it
type DataWithInfo struct {
	Configuration cioperatorapi.ReleaseBuildConfiguration
	Info          Info
}

func (i *DataWithInfo) Logger() *logrus.Entry {
	return LoggerForInfo(i.Info)
}

func (i *DataWithInfo) CommitTo(dir string) error {
	raw, err := yaml.Marshal(i.Configuration)
	if err != nil {
		i.Logger().WithError(err).Error("failed to marshal output CI Operator configuration")
		return err
	}
	outputFile := path.Join(dir, i.Info.Org, i.Info.Repo, i.Info.Basename())
	if err := ioutil.WriteFile(outputFile, raw, 0664); err != nil {
		i.Logger().WithError(err).Error("failed to write new CI Operator configuration")
		return err
	}
	return nil
}

// ByFilename stores CI Operator configurations with their metadata by filename
type ByFilename map[string]DataWithInfo

func (all ByFilename) add(handledConfig *cioperatorapi.ReleaseBuildConfiguration, handledElements *Info) error {
	all[handledElements.Basename()] = DataWithInfo{
		Configuration: *handledConfig,
		Info:          *handledElements,
	}
	return nil
}

func LoadConfigByFilename(path string) (ByFilename, error) {
	config := ByFilename{}
	if err := OperateOnCIOperatorConfigDir(path, config.add); err != nil {
		return nil, err
	}

	return config, nil
}
