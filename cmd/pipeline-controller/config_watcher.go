package main

import (
	"os"
	"sync"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"
	"gopkg.in/yaml.v2"
)

// RepoItem r  epresents a repository configuration that can be either a string or an object
type RepoItem struct {
	Name string
	Mode struct {
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
		Name string `yaml:"name"`
		Mode struct {
			Trigger string `yaml:"trigger"`
		} `yaml:"mode,omitempty"`
	}
	var raw rawRepo
	if err := unmarshal(&raw); err != nil {
		return err
	}

	r.Name = raw.Name
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
	Trigger string
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
	fileWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		w.logger.Fatal(err)
	}

	defer fileWatcher.Close()

	err = fileWatcher.Add(w.filePath)
	if err != nil {
		w.logger.Fatal(err)
	}

	err = w.reloadConfig()
	if err != nil {
		w.logger.WithError(err)
	}

	for {
		event := <-fileWatcher.Events
		if event.Op&fsnotify.Write == fsnotify.Write {
			err = w.reloadConfig()
			if err != nil {
				w.logger.WithError(err)
			}
		}

	}
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
				Trigger: repo.Mode.Trigger,
			}
		}
		ret[org.Org] = repoConfigs
	}
	return ret
}
