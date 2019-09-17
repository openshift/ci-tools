package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	types "github.com/openshift/ci-tools/pkg/steps/types"
)

type options struct {
	resolver   registry.Resolver
	configPath string
	regPath    string
}

type coalescer struct {
	sync.Mutex
	loading bool
	loader  *sync.Cond
	latest  *registry.Resolver
}

func (c *coalescer) reload(o options) {
	c.Lock()
	if c.loading {
		// someone else is loading it, so we wait
		log.Debug("Registry already being reloaded; waiting")
		c.loader.L.Lock()
		c.Unlock()
		c.loader.Wait()
		c.loader.L.Unlock()
		log.Debug("Registry reloaded by separate goroutine, returning")
		return
	}
	// nobody else is loading
	c.loading = true
	c.Unlock()
	log.Debug("Reloading registry")

	// wait 1 second to allow all fsnotify events to be processed; this
	// reduces the amount of times we reload the registry due to new events
	time.Sleep(time.Second)

	// reload the registry
	references, chain, workflows, err := load.Registry(o.regPath)
	if err != nil {
		log.Errorf("Failed to load updated registry")
		c.loader.L.Lock()
		c.loader.Broadcast()
		c.loader.L.Unlock()
		return
	}
	resolver := registry.NewResolver(references, chain, workflows)
	c.Lock()
	c.latest = &resolver
	c.Unlock()
	log.Info("Registry reloaded")

	// inform the waiters that we are done
	c.loader.L.Lock()
	c.loading = false
	c.loader.Broadcast()
	c.loader.L.Unlock()
}

func resolveConfig(o *options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		info.Filename = filepath.Join(o.configPath, branch, info.Basename())

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
		config.Tests, err = resolveConfigExternal(config.Tests, o)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "config resolution failed: %v", err)
			return
		}
		jsonConfig, err := json.Marshal(config)
		w.WriteHeader(http.StatusOK)
		w.Write(jsonConfig)
	}
}

func resolveConfigExternal(config []api.TestStepConfiguration, o *options) ([]api.TestStepConfiguration, error) {
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
		resolvedFlow, err := o.resolver.Resolve(*step.MultiStageTestConfiguration)
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

func reloadRegistry(o *options) error {
	references, chain, workflows, err := load.Registry(o.regPath)
	if err != nil {
		return err
	}
	o.resolver = registry.NewResolver(references, chain, workflows)
	return nil
}

func populateWatcher(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, file os.FileInfo, err error) error {
		// We only need to watch directories as creation, deletion, and writes
		// for files in a directory trigger events for the directory
		if file != nil && file.IsDir() {
			//log.Debugf("Adding %s to watch list", path)
			err = watcher.Add(path)
			if err != nil {
				return fmt.Errorf("Failed to add watch on directory %s: %v", path, err)
			}
		}
		return nil
	})
}

func main() {
	opts := options{}
	flag.StringVar(&opts.regPath, "registry", "", "Path to step registry")
	flag.StringVar(&opts.configPath, "config", "", "Path to config dirs")
	logLevel := flag.String("log-level", "info", "Level at which to log output.")
	flag.Parse()
	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Fatalf("invalid --log-level: %v", err)
	}
	log.SetLevel(level)

	c := coalescer{loader: sync.NewCond(&sync.Mutex{})}
	c.reload(opts)

	// watch for changes in config directories
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create new watcher: %v", err)
	}
	defer watcher.Close()
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				//log.Debugf("Received %v event for %s", event.Op, event.Name)
				if event.Op == fsnotify.Remove {
					//log.Debugf("Removing %s from watches", event.Name)
					watcher.Remove(event.Name)
				}
				// reload registry
				go c.reload(opts)
				// add new files to be watched
				if err := populateWatcher(watcher, opts.regPath); err != nil {
					log.Errorf("Failed to update fsnotify watchlist: %v", err)
				}
			case err := <-watcher.Errors:
				log.Errorf("error: %v", err)
			}
		}
	}()
	populateWatcher(watcher, opts.regPath)

	http.HandleFunc("/config", resolveConfig(&opts))
	http.ListenAndServe(":8080", nil)
}
