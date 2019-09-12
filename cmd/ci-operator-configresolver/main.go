package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	types "github.com/openshift/ci-tools/pkg/steps/types"
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
	org := r.URL.Query().Get("org")
	if org == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("org query missing or incorrect"))
		return
	}
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("repo query missing or incorrect"))
		return
	}
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("branch query missing or incorrect"))
		return
	}
	variant := r.URL.Query().Get("variant")
	info := config.Info{
		Org:     org,
		Repo:    repo,
		Branch:  branch,
		Variant: variant,
	}
	info.Filename = filepath.Join(configPath, branch, info.Basename())

	config, err := load.Config(info.Filename)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "could not load config: %v", err)
		return
	}
	if err := config.Validate(info.Org, info.Repo); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "config validation failed: %v", err)
		return
	}
	config.Tests, err = resolveConfigExternal(config.Tests)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "config resolution failed: %v", err)
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
	flag.StringVar(&regPath, "registry", "", "Path to step registry")
	flag.StringVar(&configPath, "config", "", "Path to config dirs")
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
	http.ListenAndServe(":8080", nil)
}
