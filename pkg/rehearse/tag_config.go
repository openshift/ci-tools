package rehearse

import (
	"fmt"
	"io/ioutil"

	"sigs.k8s.io/yaml"
)

// RehearsalTagConfig is the top-level structure for the tag configuration file.
type RehearsalTagConfig struct {
	Tags []Tag `json:"tags"`
}

// Tag defines a single rehearsal tag and its selectors.
type Tag struct {
	Name      string     `json:"name"`
	Selectors []Selector `json:"selectors"`
}

// Selector defines the criteria for a job to be included in a tag.
// A job must match at least one selector in a tag's list.
type Selector struct {
	JobNamePattern  string `json:"job_name_pattern,omitempty"`
	FilePathPattern string `json:"file_path_pattern,omitempty"`
	ClusterProfile  string `json:"cluster_profile,omitempty"`
	JobName         string `json:"job_name,omitempty"`
}

// LoadRehearsalTagConfig loads a rehearsal tag config from a file.
func LoadRehearsalTagConfig(path string) (*RehearsalTagConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read file %s: %w", path, err)
	}
	var config RehearsalTagConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("could not unmarshal config: %w", err)
	}
	return &config, nil
}

// HasTag returns true if the given tag name exists in the configuration.
func (c *RehearsalTagConfig) HasTag(tagName string) bool {
	for _, tag := range c.Tags {
		if tag.Name == tagName {
			return true
		}
	}
	return false
}
