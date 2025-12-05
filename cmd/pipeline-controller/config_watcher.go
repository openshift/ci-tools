package main

import (
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// RepoItem represents a repository configuration that can be either a string or an object
type RepoItem struct {
	Name     string
	Branches []string
	Mode     struct {
		Trigger string
	}
}

// UnmarshalYAML implements custom unmarshaling to support both string and object formats
func (r *RepoItem) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try to unmarshal as a string first (backwards compatibility)
	var repoString string
	if err := unmarshal(&repoString); err == nil {
		r.Name = repoString
		r.Mode.Trigger = "auto" // default to auto for backwards compatibility
		return nil
	}

	// If string unmarshaling failed, try as a struct
	type rawRepo struct {
		Name     string   `yaml:"name"`
		Branches []string `yaml:"branches,omitempty"`
		Mode     struct {
			Trigger string `yaml:"trigger"`
		} `yaml:"mode,omitempty"`
	}
	var raw rawRepo
	if err := unmarshal(&raw); err != nil {
		return err
	}

	r.Name = raw.Name
	r.Branches = raw.Branches
	r.Mode.Trigger = raw.Mode.Trigger
	if r.Mode.Trigger == "" {
		r.Mode.Trigger = "auto" // default to auto if not specified
	}
	return nil
}

// enabled config struct represents the YAML file structure of enabled repos and orgs
type enabledConfig struct {
	Orgs []struct {
		Org   string     `yaml:"org"`
		Repos []RepoItem `yaml:"repos"`
	} `yaml:"orgs"`
}

// RepoConfig contains configuration for a single repository
type RepoConfig struct {
	Trigger  string
	Branches []string // If empty, all branches are enabled
}

// watcher struct encapsulates the file watcher and configuration
type watcher struct {
	filePath string
	config   enabledConfig
	mutex    sync.Mutex
	logger   *logrus.Entry
}

func newWatcher(filePath string, logger *logrus.Entry) *watcher {
	watcher := &watcher{
		filePath: filePath,
		logger:   logger,
	}

	return watcher
}

func (w *watcher) watch() {
	// Load initial config
	if err := w.reloadConfig(); err != nil {
		w.logger.WithError(err).Error("Failed to load initial config")
	}

	// Use polling instead of fsnotify because git-sync doesn't trigger filesystem events
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	// Store previous config for comparison
	prevConfig := w.getConfigCopy()

	for range ticker.C {
		if err := w.reloadConfig(); err != nil {
			w.logger.WithError(err).Error("Failed to reload config")
			continue
		}

		currentConfig := w.getConfigCopy()
		if !reflect.DeepEqual(currentConfig, prevConfig) {
			w.logger.Info("Config change detected, config reloaded successfully")
			prevConfig = currentConfig
		}
	}
}

// getConfigCopy returns a deep copy of the current config for comparison
func (w *watcher) getConfigCopy() enabledConfig {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.config
}

func (w *watcher) reloadConfig() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	yamlFile, err := os.Open(w.filePath)
	if err != nil {
		return err
	}

	defer yamlFile.Close()

	decoder := yaml.NewDecoder(yamlFile)
	err = decoder.Decode(&w.config)
	if err != nil {
		return err
	}

	return nil
}

func (w *watcher) getConfig() map[string]map[string]RepoConfig {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	ret := map[string]map[string]RepoConfig{}
	for _, org := range w.config.Orgs {
		repoConfigs := map[string]RepoConfig{}
		for _, repo := range org.Repos {
			repoConfigs[repo.Name] = RepoConfig{
				Trigger:  repo.Mode.Trigger,
				Branches: repo.Branches,
			}
		}
		ret[org.Org] = repoConfigs
	}
	return ret
}
