/*
Copyright 2018 The Kubernetes Authors.

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

// Package secret implements an agent to read and reload the secrets.
package secret

import (
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/secretutil"
)

// secretAgent is the singleton that loads secrets for us
var secretAgent *agent

func init() {
	secretAgent = &agent{
		RWMutex:           sync.RWMutex{},
		secretsMap:        map[string][]byte{},
		ReloadingCensorer: secretutil.NewCensorer(),
	}
	logrus.SetFormatter(logrusutil.NewFormatterWithCensor(logrus.StandardLogger().Formatter, secretAgent.ReloadingCensorer))
}

// Start creates goroutines to monitor the files that contain the secret value.
// Additionally, Start wraps the current standard logger formatter with a
// censoring formatter that removes secret occurrences from the logs.
func (a *agent) Start(paths []string) error {
	secretsMap, err := loadSecrets(paths)
	if err != nil {
		return err
	}

	a.secretsMap = secretsMap
	a.ReloadingCensorer = secretutil.NewCensorer()
	a.refreshCensorer()

	logrus.SetFormatter(logrusutil.NewFormatterWithCensor(logrus.StandardLogger().Formatter, a.ReloadingCensorer))

	// Start one goroutine for each file to monitor and update the secret's values.
	for secretPath := range secretsMap {
		go a.reloadSecret(secretPath)
	}

	return nil
}

// Add registers a new path to the agent.
func Add(paths ...string) error {
	secrets, err := loadSecrets(paths)
	if err != nil {
		return err
	}

	for path, value := range secrets {
		secretAgent.setSecret(path, value)
		// Start one goroutine for each file to monitor and update the secret's values.
		go secretAgent.reloadSecret(path)
	}
	return nil
}

// GetSecret returns the value of a secret stored in a map.
func GetSecret(secretPath string) []byte {
	return secretAgent.GetSecret(secretPath)
}

// GetTokenGenerator returns a function that gets the value of a given secret.
func GetTokenGenerator(secretPath string) func() []byte {
	return func() []byte {
		return GetSecret(secretPath)
	}
}

func Censor(content []byte) []byte {
	return secretAgent.Censor(content)
}

// agent watches a path and automatically loads the secrets stored.
type agent struct {
	sync.RWMutex
	secretsMap map[string][]byte
	*secretutil.ReloadingCensorer
}

// Add registers a new path to the agent.
func (a *agent) Add(path string) error {
	secret, err := loadSingleSecret(path)
	if err != nil {
		return err
	}

	a.setSecret(path, secret)

	// Start one goroutine for each file to monitor and update the secret's values.
	go a.reloadSecret(path)
	return nil
}

// reloadSecret will begin polling the secret file at the path. If the first load
// fails, Start with return the error and abort. Future load failures will log
// the failure message but continue attempting to load.
func (a *agent) reloadSecret(secretPath string) {
	var lastModTime time.Time
	logger := logrus.NewEntry(logrus.StandardLogger())

	skips := 0
	for range time.Tick(1 * time.Second) {
		if skips < 600 {
			// Check if the file changed to see if it needs to be re-read.
			secretStat, err := os.Stat(secretPath)
			if err != nil {
				logger.WithField("secret-path", secretPath).
					WithError(err).Error("Error loading secret file.")
				continue
			}

			recentModTime := secretStat.ModTime()
			if !recentModTime.After(lastModTime) {
				skips++
				continue // file hasn't been modified
			}
			lastModTime = recentModTime
		}

		if secretValue, err := loadSingleSecret(secretPath); err != nil {
			logger.WithField("secret-path: ", secretPath).
				WithError(err).Error("Error loading secret.")
		} else {
			a.setSecret(secretPath, secretValue)
			skips = 0
		}
	}
}

// GetSecret returns the value of a secret stored in a map.
func (a *agent) GetSecret(secretPath string) []byte {
	a.RLock()
	defer a.RUnlock()
	return a.secretsMap[secretPath]
}

// setSecret sets a value in a map of secrets.
func (a *agent) setSecret(secretPath string, secretValue []byte) {
	a.Lock()
	defer a.Unlock()
	a.secretsMap[secretPath] = secretValue
	a.refreshCensorer()
}

// refreshCensorer should be called when the lock is held and the secrets map changes
func (a *agent) refreshCensorer() {
	var secrets [][]byte
	for _, value := range a.secretsMap {
		secrets = append(secrets, value)
	}
	a.ReloadingCensorer.RefreshBytes(secrets...)
}

// GetTokenGenerator returns a function that gets the value of a given secret.
func (a *agent) GetTokenGenerator(secretPath string) func() []byte {
	return func() []byte {
		return a.GetSecret(secretPath)
	}
}

// Censor replaces sensitive parts of the content with a placeholder.
func (a *agent) Censor(content []byte) []byte {
	a.RLock()
	defer a.RUnlock()
	if a.ReloadingCensorer == nil {
		// there's no constructor for an agent so we can't ensure that everyone is
		// trying to censor *after* actually loading a secret ...
		return content
	}
	return secretutil.AdaptCensorer(a.ReloadingCensorer)(content)
}

func (a *agent) getSecrets() sets.String {
	a.RLock()
	defer a.RUnlock()
	secrets := sets.NewString()
	for _, v := range a.secretsMap {
		secrets.Insert(string(v))
	}
	return secrets
}
