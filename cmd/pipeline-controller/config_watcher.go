package main

import (
	"os"
	"sync"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"
	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/sets"
)

// enabled config struct represents the YAML file structure of enabled repos and orgs
type enabledConfig struct {
	Orgs []struct {
		Org   string   `yaml:"org"`
		Repos []string `yaml:"repos"`
	} `yaml:"orgs"`
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

func (w *watcher) getConfig() map[string]sets.String {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	ret := map[string]sets.String{}
	for _, org := range w.config.Orgs {
		repos := sets.NewString(org.Repos...)
		ret[org.Org] = repos
	}
	return ret

}
