package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	types "github.com/openshift/ci-tools/pkg/steps/types"
	log "github.com/sirupsen/logrus"
)

var (
	resolver   registry.Resolver
	configPath string
	regPath    string
)

func resolveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
		return
	}
	query := r.URL.Query()
	org, ok := query["org"]
	if !ok || len(org) != 1 {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("org query missing or incorrect"))
		return
	}
	repo, ok := query["repo"]
	if !ok || len(repo) != 1 {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("repo query missing or incorrect"))
		return
	}
	branch, ok := query["branch"]
	if !ok || len(branch) != 1 {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("branch query missing or incorrect"))
		return
	}
	path := filepath.Join(configPath, branch[0], fmt.Sprintf("%s-%s-%s.yaml", org[0], repo[0], branch[0]))
	config, err := load.Config(path)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf("could not load config: %v", err)))
		return
	}
	if err := config.Validate(org[0], repo[0]); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf("config validation failed: %v", err)))
		return
	}
	config.Tests, err = resolveConfigExternal(config.Tests)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf("config resolution failed: %v", err)))
		return
	}
	jsonConfig, err := json.Marshal(config)
	w.WriteHeader(http.StatusOK)
	w.Write(jsonConfig)
}

func resolveConfigExternal(config []api.TestStepConfiguration) ([]api.TestStepConfiguration, error) {
	var result []api.TestStepConfiguration
	for _, step := range config {
		if step.MultiStageTestConfiguration == nil {
			result = append(result, step)
			continue
		}
		newStep := api.TestStepConfiguration{
			As:          step.As,
			Commands:    step.Commands,
			ArtifactDir: step.ArtifactDir,
			Secret:      step.Secret,
		}
		resolvedFlow, err := resolver.Resolve(*step.MultiStageTestConfiguration)
		if err != nil {
			return result, err
		}
		multiStageConfig := api.MultiStageTestConfiguration{
			ClusterProfile: resolvedFlow.ClusterProfile,
			Pre:            internalStepsToAPISteps(resolvedFlow.Pre),
			Test:           internalStepsToAPISteps(resolvedFlow.Test),
			Post:           internalStepsToAPISteps(resolvedFlow.Post),
		}
		newStep.MultiStageTestConfiguration = &multiStageConfig
		result = append(result, newStep)
	}
	return result, nil
}

func internalStepsToAPISteps(steps []types.TestStep) []api.TestStep {
	var newSteps []api.TestStep
	for _, step := range steps {
		newStep := api.TestStep{
			LiteralTestStep: &api.LiteralTestStep{
				As:          step.As,
				From:        step.From,
				Commands:    step.Commands,
				ArtifactDir: step.ArtifactDir,
				Resources:   step.Resources,
			},
		}
		newSteps = append(newSteps, newStep)
	}
	return newSteps
}

func reloadRegistry(path string) error {
	references, chain, workflows, err := load.Registry(path)
	if err != nil {
		return err
	}
	resolver = registry.NewResolver(references, chain, workflows)
	return nil
}

func populateWatcher(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, file os.FileInfo, err error) error {
		// We only need to watch directories as creation, deletion, and writes
		// for files in a directory trigger events for the directory
		if file.IsDir() {
			log.Infof("Adding %s to watch list", path)
			err = watcher.Add(path)
			if err != nil {
				return fmt.Errorf("Failed to add watch on directory %s: %v", path, err)
			}
		}
		return nil
	})
}

func main() {
	flag.StringVar(&regPath, "registry", "/registry", "Path to step registry")
	flag.StringVar(&configPath, "config", "/config", "Path to config dirs")
	flag.Parse()
	reloadRegistry(regPath)

	// watch for changes in config directories
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create new watcher: %v", err)
	}
	defer watcher.Close()
	go func() {
		for {
			select {
			case _ = <-watcher.Events:
				log.Info("Reloading registry")
				if err := reloadRegistry(regPath); err != nil {
					log.Fatalf("Failed to reload registry: %v", err)
				}
				log.Info("Registry successfully reloaded")
				// add new files to be watched
				if err := populateWatcher(watcher, regPath); err != nil {
					log.Fatalf("Failed to update fsnotify watchlist: %v", err)
				}
			case err := <-watcher.Errors:
				log.Println("error:", err)
			}
		}
	}()
	populateWatcher(watcher, regPath)

	http.HandleFunc("/config", resolveConfig)
	http.ListenAndServe(":80", nil)
}
