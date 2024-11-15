package dispatcher

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

// ClusterInfo holds the provider, capacity, and capabilities.
type ClusterInfo struct {
	Provider     string
	Capacity     int
	Capabilities []string
}

// ClusterMap maps a cluster name to its corresponding ClusterInfo.
type ClusterMap map[string]ClusterInfo

// Config is the configuration file of this tools, which defines the cluster parameter for each Prow job, i.e., where it runs
type Config struct {
	// the job will be run on the same cloud as the one for the e2e test
	DetermineE2EByJob bool `json:"determineE2EByJob,omitempty"`
	// the job will be run on the target cloud if it otherwise runs on the source cloud.
	// The field has effect only when DetermineE2EByJob is true.
	CloudMapping map[api.Cloud]api.Cloud `json:"cloudMapping,omitempty"`
	// the cluster cluster name if no other condition matches
	Default api.Cluster `json:"default"`
	// the cluster name for ssh bastion jobs
	SSHBastion api.Cluster `json:"sshBastion"`
	// the cluster names for kvm jobs
	KVM []api.Cluster `json:"kvm"`
	// the cluster names for no-builds jobs
	NoBuilds []api.Cluster `json:"noBuilds,omitempty"`
	// Groups maps a group of jobs to a cluster
	Groups JobGroups `json:"groups"`
	// BuildFarm maps groups of jobs to a cloud provider, like GCP
	BuildFarm map[api.Cloud]map[api.Cluster]*BuildFarmConfig `json:"buildFarm,omitempty"`
	// BuildFarmCloud maps sets of clusters to a cloud provider, like GCP
	BuildFarmCloud map[api.Cloud][]string `json:"-"`
}

type BuildFarmConfig struct {
	FilenamesRaw []string         `json:"filenames,omitempty"`
	Filenames    sets.Set[string] `json:"-"`
}

// JobGroups maps a group of jobs to a cluster
type JobGroups = map[api.Cluster]Group

// Group is a group of jobs
type Group struct {
	// a list of job names
	Jobs []string `json:"jobs,omitempty"`
	// a list of regexes of the file paths
	Paths []string `json:"paths,omitempty"`

	PathREs []*regexp.Regexp `json:"-"`
}

// GetClusterForJob returns a cluster for a prow job
func (config *Config) GetClusterForJob(jobBase prowconfig.JobBase, path string, cm ClusterMap) (api.Cluster, error) {
	cluster, _, err := config.DetermineClusterForJob(jobBase, path, cm)
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

var (
	knownCloudProviders = sets.New[string](string(api.CloudAWS), string(api.CloudGCP))
)

// DetermineCloud determines which cloud this job should run.
// It returns the value of ci-operator.openshift.io/cloud if it is none empty.
// The label is set by prow-gen for multistage tests.
// For template tests and hand-crafted tests, it returns the value of env. var. CLUSTER_TYPE from the job's spec.
func DetermineCloud(jobBase prowconfig.JobBase) string {
	labels := jobBase.Labels
	if labels != nil {
		if v, ok := labels[api.CloudLabel]; ok && v != "" {
			return v
		}
	}

	if jobBase.Spec == nil {
		return ""
	}
	for _, c := range jobBase.Spec.Containers {
		for _, e := range c.Env {
			if e.Name == "CLUSTER_TYPE" {
				if knownCloudProviders.Has(e.Value) {
					return e.Value
				}
			}
		}
	}
	return ""
}

func extractRequiredCapabilities(labels map[string]string) []string {
	var capabilities []string
	for key, value := range labels {
		if strings.HasPrefix(key, "capability/") && strings.TrimPrefix(key, "capability/") == value {
			capabilities = append(capabilities, value)
		}
	}
	return capabilities
}

func matchesAllCapabilities(clusterCapabilities, requiredCapabilities []string) bool {
	capabilitiesSet := make(map[string]bool)
	for _, cap := range clusterCapabilities {
		capabilitiesSet[cap] = true
	}

	for _, reqCap := range requiredCapabilities {
		if !capabilitiesSet[reqCap] {
			return false
		}
	}
	return true
}

// DetermineClusterForJob return the cluster for a prow job and if it can be relocated to a cluster in build farm
func (config *Config) DetermineClusterForJob(jobBase prowconfig.JobBase, path string, cm ClusterMap) (clusterName api.Cluster, mayBeRelocated bool, _ error) {
	if jobBase.Agent != "kubernetes" && jobBase.Agent != "" {
		return "", false, nil
	}
	if strings.Contains(jobBase.Name, "vsphere") && !isApplyConfigJob(jobBase) {
		return api.ClusterVSphere02, false, nil
	}
	if isSSHBastionJob(jobBase) && config.SSHBastion != "" {
		return config.SSHBastion, false, nil
	}
	if jobBase.Labels != nil {

		requiredCapabilities := extractRequiredCapabilities(jobBase.Labels)
		if len(requiredCapabilities) > 0 {
			matchingClusters := []string{}
			matchingClustersByProvider := map[string][]string{}

			for clusterName, clusterInfo := range cm {
				if matchesAllCapabilities(clusterInfo.Capabilities, requiredCapabilities) {
					matchingClusters = append(matchingClusters, clusterName)
					provider := clusterInfo.Provider
					matchingClustersByProvider[provider] = append(matchingClustersByProvider[provider], clusterName)
				}
			}
			if config.DetermineE2EByJob {
				if cloud := config.DetermineCloudMapping(jobBase); cloud != "" {
					if clusters, ok := matchingClustersByProvider[cloud]; ok {
						if len(clusters) > 0 {
							// as in other places in this file, use this method to have basic deterministic distribution
							return api.Cluster(clusters[len(filepath.Base(path))%len(clusters)]), false, nil
						}
					}
				}
			}
			if len(matchingClusters) == 0 {
				sort.Strings(requiredCapabilities)
				return "", false, fmt.Errorf("job %s can't be matched with any cluster using provided capabilities: %s", jobBase.Name, strings.Join(requiredCapabilities, ","))
			}
			// as in other places in this file, use this method to have basic deterministic distribution
			return api.Cluster(matchingClusters[len(filepath.Base(path))%len(matchingClusters)]), false, nil

		}

		if _, ok := jobBase.Labels[api.KVMDeviceLabel]; ok && len(config.KVM) > 0 {
			// Any deterministic distribution is fine for now.
			// We could implement more effective distribution when we understand more about the jobs.
			return config.KVM[len(filepath.Base(path))%len(config.KVM)], false, nil
		}
		if cluster, ok := jobBase.Labels[api.ClusterLabel]; ok {
			return api.Cluster(cluster), false, nil
		}
	}

	if config.DetermineE2EByJob {
		if cloud := config.DetermineCloudMapping(jobBase); cloud != "" {
			if clusters, ok := config.BuildFarmCloud[api.Cloud(cloud)]; ok {
				if len(clusters) > 0 {
					return api.Cluster(clusters[len(filepath.Base(path))%len(clusters)]), false, nil
				}
			}
		}
	}

	if jobBase.Labels != nil {
		if _, ok := jobBase.Labels[api.NoBuildsLabel]; ok && len(config.NoBuilds) > 0 {
			// Any deterministic distribution is fine for now.
			return config.NoBuilds[len(filepath.Base(path))%len(config.NoBuilds)], false, nil
		}
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
		for cluster, filenames := range v {
			filename := filepath.Base(path)
			if filenames.Filenames.Has(filename) {
				if clusterName == "" {
					clusterName = cluster
					mayBeRelocated = true
				}
				matches = append(matches, filename)
			}
		}
	}
	// sort for tests
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

// DetermineCloudMapping determines if for a given cloud there is a replacement to map, eg for cost saving reasons
func (config *Config) DetermineCloudMapping(jobBase prowconfig.JobBase) string {
	cloud := DetermineCloud(jobBase)
	if mapping, ok := config.CloudMapping[api.Cloud(cloud)]; ok {
		cloud = string(mapping)
	}
	return cloud
}

// IsInBuildFarm returns the cloudProvider if the cluster is in the build farm; empty string otherwise.
func (config *Config) IsInBuildFarm(clusterName api.Cluster) api.Cloud {
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
		if config.BuildFarmCloud == nil {
			config.BuildFarmCloud = map[api.Cloud][]string{}
		}
		clusters := sets.New[string]()
		for cluster, filenames := range config.BuildFarm[cloudProvider] {
			clusters.Insert(string(cluster))
			filenames.Filenames = sets.New[string]()
			for _, f := range filenames.FilenamesRaw {
				filenames.Filenames.Insert(f)
			}
			config.BuildFarm[cloudProvider][cluster] = filenames
		}
		config.BuildFarmCloud[cloudProvider] = sets.List(clusters)
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
	var matches []string
	for k, v := range records {
		if v > 1 {
			matches = append(matches, k)
		}
	}
	// sort for tests
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
	err = os.WriteFile(configPath, bytes, 0644)
	if err != nil {
		return err
	}
	return nil
}

// GetBuildFarmSize returns build farm size
func (config *Config) GetBuildFarmSize() int {
	size := 0

	if config == nil || config.BuildFarm == nil {
		return size
	}
	for cloud := range config.BuildFarm {
		for range config.BuildFarm[cloud] {
			size++
		}
	}
	return size
}
