package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/plugins"
)

const (
	externalPluginName     = "repo-brancher-controller"
	externalPluginEndpoint = "http://repo-brancher-controller"
)

type missingExternalPluginRegistrationError struct {
	repository string
}

func (e missingExternalPluginRegistrationError) Error() string {
	return fmt.Sprintf("%s: missing %s push registration at %s", e.repository, externalPluginName, externalPluginEndpoint)
}

// validateExternalPluginRegistrations verifies that every repository in desired is covered by a Prow push-event registration for this external plugin.
// pluginConfigDir is the path to the directory containing Prow's sharded plugin configuration.
// desired maps repository and source-branch keys to their sets of target branches.
// It returns an error if the plugin configuration cannot be loaded or if any desired repository lacks the required registration.
func validateExternalPluginRegistrations(pluginConfigDir string, desired map[repoKey]sets.Set[string]) error {
	agent := &plugins.ConfigAgent{}
	if err := agent.Load(filepath.Join(pluginConfigDir, "_plugins.yaml"), []string{pluginConfigDir}, "_pluginconfig.yaml", false, true); err != nil {
		return fmt.Errorf("load Prow plugin configuration: %w", err)
	}
	pluginConfig := agent.Config()

	repositories := sets.New[string]()
	for key := range desired {
		repositories.Insert(key.org + "/" + key.repo)
	}
	ordered := repositories.UnsortedList()
	sort.Strings(ordered)
	var errs []error
	for _, repository := range ordered {
		org := strings.SplitN(repository, "/", 2)[0]
		configured := append([]plugins.ExternalPlugin{}, pluginConfig.ExternalPlugins[org]...)
		configured = append(configured, pluginConfig.ExternalPlugins[repository]...)
		registered := false
		for _, plugin := range configured {
			if plugin.Name == externalPluginName && strings.TrimRight(plugin.Endpoint, "/") == externalPluginEndpoint && sets.New(plugin.Events...).Has("push") {
				registered = true
				break
			}
		}
		if !registered {
			errs = append(errs, missingExternalPluginRegistrationError{repository: repository})
		}
	}
	return errors.Join(errs...)
}
