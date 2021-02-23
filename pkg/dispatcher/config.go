package dispatcher

import (
	"fmt"
	"io/ioutil"
	"regexp"
	"sort"
	"strings"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowconfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

// CloudProvider define cloud providers
type CloudProvider string

const (
	// CloudAWS is the cloud provider AWS
	CloudAWS CloudProvider = "aws"
	// CloudGCP is the cloud provider GCP
	CloudGCP CloudProvider = "gcp"
)

// Config is the configuration file of this tools, which defines the cluster parameter for each Prow job, i.e., where it runs
type Config struct {
	// the cluster cluster name if no other condition matches
	Default ClusterName `json:"default"`
	// the cluster cluster name for ssh bastion jobs
	SSHBastion ClusterName `json:"sshBastion"`
	// Groups maps a group of jobs to a cluster
	Groups JobGroups `json:"groups"`
	// BuildFarm maps groups of jobs to a cloud provider, like GCP
	BuildFarm map[CloudProvider]JobGroups `json:"buildFarm,omitempty"`
}

// ClusterName is the name of a cluster
type ClusterName string

const (
	// ClusterAPICI is the cluster api.ci which will be deprecated soon
	ClusterAPICI ClusterName = "api.ci"
	// ClusterAPPCI is the cluster app.ci which runs Prow
	ClusterAPPCI ClusterName = "app.ci"
	// ClusterBuild01 is the cluster build01 in the build farm
	ClusterBuild01 ClusterName = "build01"
	// ClusterBuild02 is the cluster build02 in the build farm
	ClusterBuild02 ClusterName = "build02"
	ClusterVSphere ClusterName = "vsphere"
)

// JobGroups maps a group of jobs to a cluster
type JobGroups = map[ClusterName]Group

//Group is a group of jobs
type Group struct {
	// a list of job names
	Jobs []string `json:"jobs,omitempty"`
	// a list of regexes of the file paths
	Paths []string `json:"paths,omitempty"`

	PathREs []*regexp.Regexp `json:"-"`
}

// GetClusterForJob returns a cluster for a prow job
func (config *Config) GetClusterForJob(jobBase prowconfig.JobBase, path string) (ClusterName, error) {
	cluster, _, err := config.DetermineClusterForJob(jobBase, path)
	return cluster, err
}

func isApplyConfigJob(jobBase prowconfig.JobBase) bool {
	if jobBase.Spec == nil {
		return false
	}
	containers := jobBase.Spec.Containers
	// exclude applyconfig jobs
	if len(containers) == 1 && strings.Contains(containers[0].Image, "applyconfig") {
		return true
	}
	return false
}

// DetermineClusterForJob return the cluster for a prow job and if it can be relocated to a cluster in build farm
func (config *Config) DetermineClusterForJob(jobBase prowconfig.JobBase, path string) (clusterName ClusterName, mayBeRelocated bool, _ error) {
	if jobBase.Agent != "kubernetes" && jobBase.Agent != "" {
		return "", false, nil
	}
	if strings.Contains(jobBase.Name, "vsphere") && !isApplyConfigJob(jobBase) {
		return ClusterVSphere, false, nil
	}
	if isSSHBastionJob(jobBase) && config.SSHBastion != "" {
		return config.SSHBastion, false, nil
	}
	var matches []string
	for cluster, group := range config.Groups {
		for _, job := range group.Jobs {
			if jobBase.Name == job {
				clusterName = cluster
			}
		}
	}
	for cluster, group := range config.Groups {
		for _, re := range group.PathREs {
			if re.MatchString(path) {
				if clusterName == "" {
					clusterName = cluster
				}
				matches = append(matches, re.String())
			}
		}
	}
	for _, v := range config.BuildFarm {
		for cluster, group := range v {
			for _, job := range group.Jobs {
				if jobBase.Name == job {
					if clusterName == "" {
						clusterName = cluster
						mayBeRelocated = true
					}
				}
			}
		}
		for cluster, group := range v {
			for _, re := range group.PathREs {
				if re.MatchString(path) {
					if clusterName == "" {
						clusterName = cluster
						mayBeRelocated = true
					}
					matches = append(matches, re.String())
				}
			}
		}
	}
	//sort for tests
	sort.Strings(matches)
	if len(matches) > 1 {
		return "", false, fmt.Errorf("path %s matches more than 1 regex: %s", path, matches)
	}

	if clusterName == "" {
		clusterName = config.Default
		mayBeRelocated = true
	}
	return clusterName, mayBeRelocated, nil
}

func isSSHBastionJob(base prowconfig.JobBase) bool {
	for k := range base.Labels {
		if k == jobconfig.SSHBastionLabel {
			return true
		}
	}
	return false
}

// IsInBuildFarm returns the cloudProvider if the cluster is in the build farm; empty string otherwise.
func (config *Config) IsInBuildFarm(clusterName ClusterName) CloudProvider {
	for cloudProvider, v := range config.BuildFarm {
		for cluster := range v {
			if cluster == clusterName {
				return cloudProvider
			}
		}
	}
	return ""
}

// MatchingPathRegEx returns true if the given path matches a path regular expression defined in a config's group
func (config *Config) MatchingPathRegEx(path string) bool {
	for _, group := range config.Groups {
		for _, re := range group.PathREs {
			if re.MatchString(path) {
				return true
			}
		}
	}
	return false
}

// LoadConfig loads config from a file
func LoadConfig(configPath string) (*Config, error) {
	config := &Config{}
	data, err := gzip.ReadFileMaybeGZIP(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read the config file %q: %w", configPath, err)
	}
	err = yaml.Unmarshal(data, config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal the config %q: %w", string(data), err)
	}

	var errs []error
	for cluster, group := range config.Groups {
		var pathREs []*regexp.Regexp
		for i, p := range group.Paths {
			re, err := regexp.Compile(p)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to compile regex config.Groups[%s].Paths[%d] from %q: %w", cluster, i, p, err))
				continue
			}
			pathREs = append(pathREs, re)
		}
		group.PathREs = pathREs
		config.Groups[cluster] = group
	}

	for cloudProvider := range config.BuildFarm {
		for cluster, group := range config.BuildFarm[cloudProvider] {
			var pathREs []*regexp.Regexp
			for i, p := range group.Paths {
				re, err := regexp.Compile(p)
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to compile regex config.BuildFarm[%s][%s].Paths[%d] from %q: %w", cloudProvider, cluster, i, p, err))
					continue
				}
				pathREs = append(pathREs, re)
			}
			group.PathREs = pathREs
			config.BuildFarm[cloudProvider][cluster] = group
		}
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	return config, nil
}

// Validate checks if the config is valid
func (config *Config) Validate() error {
	if config.Default == "" {
		return fmt.Errorf("the default cluster must be set in the config")
	}
	records := map[string]int{}
	for _, group := range config.Groups {
		for _, job := range group.Jobs {
			records[job] = records[job] + 1
		}
	}
	for _, v := range config.BuildFarm {
		for _, group := range v {
			for _, job := range group.Jobs {
				records[job]++
			}
		}
	}
	var matches []string
	for k, v := range records {
		if v > 1 {
			matches = append(matches, k)
		}
	}
	//sort for tests
	sort.Strings(matches)
	if len(matches) > 1 {
		return fmt.Errorf("there are job names occurring more than once: %s", matches)
	}
	return nil
}

// SaveConfig saves config to a file
func SaveConfig(config *Config, configPath string) error {
	bytes, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(configPath, bytes, 0644)
	if err != nil {
		return err
	}
	return nil
}
