package bumper

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// SippyConfig represents the structure of the Sippy configuration file
type SippyConfig struct {
	Releases map[string]SippyRelease `yaml:"releases"`
}

// SippyRelease represents a release configuration in Sippy
type SippyRelease struct {
	InformingJobs []string `yaml:"informingJobs"`
	BlockingJobs  []string `yaml:"blockingJobs"`
}

// LoadSippyConfig loads and parses the Sippy configuration file
func LoadSippyConfig(sippyConfigPath string) (*SippyConfig, error) {
	data, err := os.ReadFile(sippyConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read sippy config: %w", err)
	}

	var config SippyConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("unmarshal sippy config: %w", err)
	}

	return &config, nil
}

// GetInformingJobsForRelease returns the list of informing jobs for a specific release
func (c *SippyConfig) GetInformingJobsForRelease(version string) ([]string, error) {
	release, ok := c.Releases[version]
	if !ok {
		return nil, fmt.Errorf("release %s not found in sippy config", version)
	}

	return release.InformingJobs, nil
}

// GetBlockingJobsForRelease returns the list of blocking jobs for a specific release
func (c *SippyConfig) GetBlockingJobsForRelease(version string) ([]string, error) {
	release, ok := c.Releases[version]
	if !ok {
		return nil, fmt.Errorf("release %s not found in sippy config", version)
	}

	return release.BlockingJobs, nil
}

// ProwJobMetadata contains metadata extracted from a Prow job definition
type ProwJobMetadata struct {
	Org     string
	Repo    string
	Branch  string
	Variant string
}

// JobNameToConfigFile maps a periodic job name to its corresponding ci-operator config file path
// by loading and parsing the Prow job metadata.
func JobNameToConfigFile(jobName, configRootDir string) (string, error) {
	if !strings.HasPrefix(jobName, "periodic-ci-") {
		return "", fmt.Errorf("job %s is not a periodic job", jobName)
	}
	jobsRootDir := filepath.Join(filepath.Dir(configRootDir), "jobs")
	metadata, err := findJobMetadata(jobName, jobsRootDir)
	if err != nil {
		return "", fmt.Errorf("find job metadata: %w", err)
	}
	configFilename := buildConfigFilename(metadata)
	configPath := filepath.Join(configRootDir, metadata.Org, metadata.Repo, configFilename)
	if _, err := os.Stat(configPath); err != nil {
		return "", fmt.Errorf("config file %s not found: %w", configPath, err)
	}

	return configPath, nil
}

func buildConfigFilename(metadata *ProwJobMetadata) string {
	if metadata.Variant != "" {
		return fmt.Sprintf("%s-%s-%s__%s.yaml", metadata.Org, metadata.Repo, metadata.Branch, metadata.Variant)
	}
	return fmt.Sprintf("%s-%s-%s.yaml", metadata.Org, metadata.Repo, metadata.Branch)
}

// findJobMetadata searches for the job definition in Prow job files and extracts metadata
func findJobMetadata(jobName, jobsRootDir string) (*ProwJobMetadata, error) {
	var metadata *ProwJobMetadata

	err := filepath.Walk(jobsRootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var jobFile struct {
			Periodics []struct {
				Name      string            `yaml:"name"`
				Labels    map[string]string `yaml:"labels"`
				ExtraRefs []struct {
					Org     string `yaml:"org"`
					Repo    string `yaml:"repo"`
					BaseRef string `yaml:"base_ref"`
				} `yaml:"extra_refs"`
			} `yaml:"periodics"`
		}

		if err := yaml.Unmarshal(data, &jobFile); err != nil {
			return nil
		}

		for _, job := range jobFile.Periodics {
			if job.Name == jobName {
				if len(job.ExtraRefs) == 0 {
					return fmt.Errorf("job %s has no extra_refs", jobName)
				}

				ref := job.ExtraRefs[0]
				variant := job.Labels["ci-operator.openshift.io/variant"]

				metadata = &ProwJobMetadata{
					Org:     ref.Org,
					Repo:    ref.Repo,
					Branch:  ref.BaseRef,
					Variant: variant,
				}

				return filepath.SkipAll
			}
		}

		return nil
	})

	if err != nil && err != filepath.SkipAll {
		return nil, err
	}

	if metadata == nil {
		return nil, fmt.Errorf("job %s not found in %s", jobName, jobsRootDir)
	}

	return metadata, nil
}

// GetConfigFilesFromSippyJobs returns a list of config file paths for the given Sippy informing jobs
func GetConfigFilesFromSippyJobs(jobs []string, configDir string) ([]string, error) {
	configFiles := make([]string, 0, len(jobs))
	seen := make(map[string]bool)
	skipped := 0

	logrus.Infof("mapping %d Sippy informing/blocking jobs to config files...", len(jobs))

	jobsRootDir := filepath.Join(filepath.Dir(configDir), "jobs")
	jobMetadataCache, err := buildJobMetadataCache(jobs, jobsRootDir)
	if err != nil {
		return nil, fmt.Errorf("build job metadata cache: %w", err)
	}

	logrus.Debugf("cached metadata for %d jobs", len(jobMetadataCache))

	for _, job := range jobs {
		metadata, found := jobMetadataCache[job]
		if !found {
			logrus.Debugf("skipping job %s: not found in cache", job)
			skipped++
			continue
		}

		configFilename := buildConfigFilename(metadata)
		configPath := filepath.Join(configDir, metadata.Org, metadata.Repo, configFilename)

		if _, err := os.Stat(configPath); err != nil {
			logrus.Debugf("skipping job %s: config file %s not found", job, configPath)
			skipped++
			continue
		}

		// Deduplicate
		if !seen[configPath] {
			configFiles = append(configFiles, configPath)
			seen[configPath] = true
		}
	}

	logrus.Infof("mapped %d jobs to %d unique config files (skipped %d jobs)", len(jobs), len(configFiles), skipped)

	return configFiles, nil
}

// buildJobMetadataCache scans all job files once and builds a cache of job name -> metadata
func buildJobMetadataCache(jobNames []string, jobsRootDir string) (map[string]*ProwJobMetadata, error) {
	cache := make(map[string]*ProwJobMetadata)
	needed := make(map[string]bool)
	for _, job := range jobNames {
		needed[job] = true
	}

	logrus.Debugf("scanning job files in %s to find %d jobs...", jobsRootDir, len(needed))

	err := filepath.Walk(jobsRootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var jobFile struct {
			Periodics []struct {
				Name      string            `yaml:"name"`
				Labels    map[string]string `yaml:"labels"`
				ExtraRefs []struct {
					Org     string `yaml:"org"`
					Repo    string `yaml:"repo"`
					BaseRef string `yaml:"base_ref"`
				} `yaml:"extra_refs"`
			} `yaml:"periodics"`
		}

		if err := yaml.Unmarshal(data, &jobFile); err != nil {
			return nil
		}

		for _, job := range jobFile.Periodics {
			// Only cache jobs we're looking for
			if !needed[job.Name] {
				continue
			}

			if len(job.ExtraRefs) == 0 {
				continue
			}

			ref := job.ExtraRefs[0]
			variant := job.Labels["ci-operator.openshift.io/variant"]

			cache[job.Name] = &ProwJobMetadata{
				Org:     ref.Org,
				Repo:    ref.Repo,
				Branch:  ref.BaseRef,
				Variant: variant,
			}

			// If we found all jobs, we can stop early
			if len(cache) == len(needed) {
				return filepath.SkipAll
			}
		}

		return nil
	})

	if err != nil && err != filepath.SkipAll {
		return nil, err
	}

	return cache, nil
}

// GetConfigFilesForReleaseFromSippy loads the Sippy config and returns config files for a specific release
// It also includes related releases like "4.21-okd" when looking for "4.21"
func GetConfigFilesForReleaseFromSippy(sippyConfigPath, releaseVersion, configDir string) ([]string, error) {
	config, err := LoadSippyConfig(sippyConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load sippy config: %w", err)
	}

	// Find all releases that match this version (e.g., "4.21", "4.21-okd", etc.)
	var allJobs []string
	relatedReleases := findRelatedReleases(config, releaseVersion)

	logrus.Infof("found %d related releases for %s: %v", len(relatedReleases), releaseVersion, relatedReleases)

	for _, release := range relatedReleases {
		jobs, err := config.GetInformingJobsForRelease(release)
		if err != nil {
			logrus.WithError(err).Warnf("failed to get informing jobs for release %s", release)
			continue
		}

		blockingJobs, err := config.GetBlockingJobsForRelease(release)
		if err != nil {
			logrus.WithError(err).Warnf("failed to get blocking jobs for release %s", release)
		} else {
			jobs = append(jobs, blockingJobs...)
		}

		logrus.Infof("release %s: %d jobs (%d informing + %d blocking)",
			release, len(jobs), len(jobs)-len(blockingJobs), len(blockingJobs))
		allJobs = append(allJobs, jobs...)
	}

	configFiles, err := GetConfigFilesFromSippyJobs(allJobs, configDir)
	if err != nil {
		return nil, fmt.Errorf("get config files from jobs: %w", err)
	}

	return configFiles, nil
}

// findRelatedReleases finds all releases that contain the given version
// e.g., for "4.21" it returns ["4.21", "4.21-okd"]
func findRelatedReleases(config *SippyConfig, version string) []string {
	var related []string
	for release := range config.Releases {
		// Exact match or starts with version followed by hyphen
		if release == version || strings.HasPrefix(release, version+"-") {
			related = append(related, release)
		}
	}
	return related
}
