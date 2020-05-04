package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
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

// DataWithInfo describes the metadata for a CI Operator configuration file
type Info struct {
	Org    string
	Repo   string
	Branch string
	// Variant allows for parallel configuration files for one (org,repo,branch)
	Variant string
	// Filename is the full path to the file on disk
	Filename string
	// OrgPath is the full path to the directory containing config for the org
	OrgPath string
	// RepoPath is the full path to the directory containing config for the repo
	RepoPath string
}

// IsComplete returns an error if at least one of Org, Repo, Branch members is
// empty, otherwise it returns nil
func (i *Info) IsComplete() error {
	var missing []string
	for item, value := range map[string]string{
		"organization": i.Org,
		"repository":   i.Repo,
		"branch":       i.Branch,
	} {
		if value == "" {
			missing = append(missing, item)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		s := ""
		if len(missing) > 1 {
			s = "s"
		}
		return fmt.Errorf("missing item%s: %s", s, strings.Join(missing, ", "))
	}

	return nil
}

// TestName returns a short name of a test defined in this file, including
// variant, if present
func (i *Info) TestName(testName string) string {
	if i.Variant == "" {
		return testName
	}
	return fmt.Sprintf("%s-%s", i.Variant, testName)
}

// JobName returns a full name of a job corresponding to a test defined in this
// file, including variant, if present
func (i *Info) JobName(prefix, name string) string {
	return fmt.Sprintf("%s-ci-%s-%s-%s-%s", prefix, i.Org, i.Repo, i.Branch, i.TestName(name))
}

// Basename returns the unique name for this file in the config
func (i *Info) Basename() string {
	basename := strings.Join([]string{i.Org, i.Repo, i.Branch}, "-")
	if i.Variant != "" {
		basename = fmt.Sprintf("%s__%s", basename, i.Variant)
	}
	return fmt.Sprintf("%s.yaml", basename)
}

// RelativePath returns the path to the config under the root config dir
func (i *Info) RelativePath() string {
	return path.Join(i.Org, i.Repo, i.Basename())
}

// ConfigMapName returns the configmap in which we expect this file to be uploaded
func (i *Info) ConfigMapName() string {
	return fmt.Sprintf("ci-operator-%s-configs", FlavorForBranch(i.Branch))
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

// IsCiopConfigCM returns true if a given name is a valid ci-operator config ConfigMap
func IsCiopConfigCM(name string) bool {
	return regexp.MustCompile(`^ci-operator-.+-configs$`).MatchString(name)
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
		Org:      org,
		Repo:     repo,
		Branch:   branch,
		Variant:  variant,
		Filename: configFilePath,
		OrgPath:  filepath.Dir(configSpecDir),
		RepoPath: configSpecDir,
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
