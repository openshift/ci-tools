/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package config

import (
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Agent watches a path and automatically loads the config stored
// therein.
type Agent struct {
	mut sync.RWMutex // do not export Lock, etc methods
	c   *Configuration
}

// Start will begin polling the config file at the path. If the first load
// fails, Start will return the error and abort. Future load failures will log
// the failure message but continue attempting to load.
func (ca *Agent) Start(configLocation string) error {
	c, err := Load(configLocation)
	if err != nil {
		return err
	}
	ca.Set(c)
	go func() {
		var lastModTime time.Time
		// Rarely, if two changes happen in the same second, mtime will
		// be the same for the second change, and an mtime-based check would
		// fail. Reload periodically just in case.
		skips := 0
		for range time.Tick(1 * time.Second) {
			if skips < 600 {
				// Check if the file changed to see if it needs to be re-read.
				// os.Stat follows symbolic links, which is how ConfigMaps work.
				stat, err := os.Stat(configLocation)
				if err != nil {
					logrus.WithField("configLocation", configLocation).WithError(err).Error("Error loading config.")
					continue
				}

				recentModTime := stat.ModTime()

				if !recentModTime.After(lastModTime) {
					skips++
					continue // file hasn't been modified
				}
				lastModTime = recentModTime
			}
			if c, err := Load(configLocation); err != nil {
				logrus.WithField("configLocation", configLocation).
					WithError(err).Error("Error loading config.")
			} else {
				skips = 0
				if !reflect.DeepEqual(c, ca.c) {
					logrus.Info("Changes of configuration detected.")
				}
				ca.Set(c)
			}
		}
	}()
	return nil
}

// Getter returns the current Config in a thread-safe manner.
type Getter func() *Configuration

// Config returns the latest config. Do not modify the config.
func (ca *Agent) Config() *Configuration {
	ca.mut.RLock()
	defer ca.mut.RUnlock()
	return ca.c
}

// Set sets the config. Useful for testing.
func (ca *Agent) Set(c *Configuration) {
	ca.mut.Lock()
	defer ca.mut.Unlock()
	ca.c = c
}
