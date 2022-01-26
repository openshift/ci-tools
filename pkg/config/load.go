package config

import (
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/util/gzip"
	"github.com/openshift/ci-tools/pkg/validation"
)

// ProwgenFile is the name of prowgen's configuration file.
var ProwgenFile = ".config.prowgen"

// Prowgen holds the information of the prowgen's configuration file.
type Prowgen struct {
	// Private indicates that generated jobs should be marked as hidden
	// from display in deck and that they should mount appropriate git credentials
	// to clone the repository under test.
	Private bool `json:"private,omitempty"`
	// Expose declares that jobs should not be hidden from view in deck if they
	// are private.
	// This field has no effect if private is not set.
	Expose bool `json:"expose,omitempty"`
}

func readCiOperatorConfig(configFilePath string, info Info) (*cioperatorapi.ReleaseBuildConfiguration, error) {
	data, err := gzip.ReadFileMaybeGZIP(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read ci-operator config (%w)", err)
	}

	var configSpec cioperatorapi.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(data, &configSpec); err != nil {
		return nil, fmt.Errorf("failed to load ci-operator config (%w)", err)
	}

	if err := validation.IsValidConfiguration(&configSpec, info.Org, info.Repo); err != nil {
		return nil, fmt.Errorf("invalid ci-operator config: %w", err)
	}

	return &configSpec, nil
}

// Info describes the metadata for a CI Operator configuration file
// along with where it's loaded from
type Info struct {
	cioperatorapi.Metadata
	// Filename is the full path to the file on disk
	Filename string
	// OrgPath is the full path to the directory containing config for the org
	OrgPath string
	// RepoPath is the full path to the directory containing config for the repo
	RepoPath string
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

	return &Info{
		Metadata: cioperatorapi.Metadata{
			Org:     org,
			Repo:    repo,
			Branch:  branch,
			Variant: variant,
		},
		Filename: configFilePath,
		OrgPath:  filepath.Dir(configSpecDir),
		RepoPath: configSpecDir,
	}, nil
}

func (i *Info) LogFields() logrus.Fields {
	return logrus.Fields{
		"org":         i.Org,
		"repo":        i.Repo,
		"branch":      i.Branch,
		"variant":     i.Variant,
		"source-file": i.Basename(),
	}
}

func isConfigFile(info fs.DirEntry) bool {
	extension := filepath.Ext(info.Name())
	return !info.IsDir() && (extension == ".yaml" || extension == ".yml")
}

// isMountSpecialFile identifies special files in Kubernetes mounts
// The general structure of a mount is:
//
//     config
//     ├── ..2019_11_15_19_57_20.547184898
//     │   ├── foo-bar-master.yaml
//     │   └── super-duper-master.yaml
//     ├── ..data -> ..2019_11_15_19_57_20.547184898
//     ├── foo-bar-master.yaml -> ..data/foo-bar-master.yaml
//     └── super-duper-master.yaml -> ..data/super-duper-master.yaml
//
// In a recursive directory traversal, paths starting with `..` are skipped so
// files are not processed twice.
func isMountSpecialFile(path string) bool {
	return strings.HasPrefix(path, "..")
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
	type item struct {
		config *cioperatorapi.ReleaseBuildConfiguration
		info   *Info
	}
	inputCh := make(chan string)
	produce := func() error {
		defer close(inputCh)
		return filepath.WalkDir(filepath.Join(configDir, subDir), func(path string, info fs.DirEntry, err error) error {
			if err != nil {
				logrus.WithField("source-file", path).WithError(err).Error("Failed to walk CI Operator configuration dir")
				// file may not exist due to race condition between the reload and k8s removing deleted/moved symlinks in a confimap directory; ignore it
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if isMountSpecialFile(info.Name()) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if isConfigFile(info) {
				inputCh <- path
			}
			return nil
		})
	}
	outputCh := make(chan item)
	errCh := make(chan error)
	map_ := func() error {
		for path := range inputCh {
			info, err := InfoFromPath(path)
			if err != nil {
				logrus.WithField("source-file", path).WithError(err).Error("Failed to resolve info from CI Operator configuration path")
				errCh <- err
				continue
			}
			config, err := readCiOperatorConfig(path, *info)
			if err != nil {
				logrus.WithField("source-file", path).WithError(err).Error("Failed to load CI Operator configuration")
				errCh <- err
				continue
			}
			if err := validation.IsValidRuntimeConfiguration(config); err != nil {
				errCh <- fmt.Errorf("invalid ci-operator config: %w", err)
				continue
			}
			outputCh <- item{config, info}
		}
		return nil
	}
	reduce := func() error {
		for i := range outputCh {
			if err := callback(i.config, i.info); err != nil {
				errCh <- err
			}
		}
		return nil
	}
	done := func() { close(outputCh) }
	return util.ProduceMapReduce(0, produce, map_, reduce, done, errCh)
}

func LoggerForInfo(info Info) *logrus.Entry {
	return logrus.WithFields(info.LogFields())
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
	outputFile := path.Join(dir, i.Info.RelativePath())
	if err := os.MkdirAll(path.Dir(outputFile), os.ModePerm); err != nil && !os.IsExist(err) {
		i.Logger().WithError(err).Error("failed to ensure directory existed for new CI Operator configuration")
		return err
	}
	if err := ioutil.WriteFile(outputFile, raw, 0664); err != nil {
		i.Logger().WithError(err).Error("failed to write new CI Operator configuration")
		return err
	}
	return nil
}

// DataByFilename stores CI Operator configurations with their metadata by filename
type DataByFilename map[string]DataWithInfo

func (all DataByFilename) add(handledConfig *cioperatorapi.ReleaseBuildConfiguration, handledElements *Info) error {
	all[handledElements.Basename()] = DataWithInfo{
		Configuration: *handledConfig,
		Info:          *handledElements,
	}
	return nil
}

func LoadDataByFilename(path string) (DataByFilename, error) {
	config := DataByFilename{}
	if err := OperateOnCIOperatorConfigDir(path, config.add); err != nil {
		return nil, err
	}

	return config, nil
}

// ByFilename stores CI Operator configurations with their metadata by filename
type ByFilename map[string]cioperatorapi.ReleaseBuildConfiguration

func (all ByFilename) add(handledConfig *cioperatorapi.ReleaseBuildConfiguration, handledElements *Info) error {
	all[handledElements.Basename()] = *handledConfig
	return nil
}

func LoadByFilename(path string) (ByFilename, error) {
	config := ByFilename{}
	if err := OperateOnCIOperatorConfigDir(path, config.add); err != nil {
		return nil, err
	}

	return config, nil
}

// ByOrgRepo maps org --> repo --> list of branched and variant configs
type ByOrgRepo map[string]map[string][]cioperatorapi.ReleaseBuildConfiguration

func (all ByOrgRepo) add(c *cioperatorapi.ReleaseBuildConfiguration, i *Info) error {
	org := all[c.Metadata.Org]
	if org == nil {
		org = make(map[string][]cioperatorapi.ReleaseBuildConfiguration)
		all[c.Metadata.Org] = org
	}
	org[c.Metadata.Repo] = append(org[c.Metadata.Repo], *c)
	return nil
}

func LoadByOrgRepo(path string) (ByOrgRepo, error) {
	config := ByOrgRepo{}
	if err := OperateOnCIOperatorConfigDir(path, config.add); err != nil {
		return nil, err
	}
	return config, nil
}
