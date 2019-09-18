package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/ghodss/yaml"
	log "github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	types "github.com/openshift/ci-tools/pkg/steps/types"
)

type options struct {
	registry   registry.Resolver
	configs    filenameToConfig
	configPath string
	regPath    string
}

type filenameToConfig map[string]api.ReleaseBuildConfiguration

type coalescer struct {
	sync.Mutex
	loading    bool
	loader     *sync.Cond
	reloadFunc func(o *options, lock sync.Locker) error
	// name for easier debugging
	name string
}

func (c *coalescer) coalesce(o *options) {
	c.Lock()
	if c.loading {
		// someone else is loading it, so we wait
		log.Debugf("Reload of %s already in progress; waiting", c.name)
		c.loader.L.Lock()
		c.Unlock()
		c.loader.Wait()
		c.loader.L.Unlock()
		log.Debugf("Finished waiting for reload of %s", c.name)
		return
	}
	// nobody else is loading
	c.loading = true
	c.Unlock()
	log.Debugf("Reloading %s", c.name)

	// reload the registry
	err := c.reloadFunc(o, &c.Mutex)
	if err != nil {
		log.Errorf("Failed to reload %s", c.name)
		c.loader.L.Lock()
		c.loader.Broadcast()
		c.loader.L.Unlock()
		return
	}
	log.Infof("Reloaded %s", c.name)

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

		config, ok := o.configs[info.Basename()]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("config does not exist"))
			return
		}
		var err error
		config.Tests, err = resolveConfigExternal(config.Tests, o)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "config resolution failed: %v", err)
			return
		}
		jsonConfig, err := json.Marshal(config)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to marshal config to JSON: %v", err)
			return
		}
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
		resolvedFlow, err := o.registry.Resolve(*step.MultiStageTestConfiguration)
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

// buildFilenameToConfig generates a new filenameToConfig map.
func buildFilenameToConfig(o *options, lock sync.Locker) error {
	log.Debug("Reloading configs")
	configs := filenameToConfig{}
	err := filepath.Walk(o.configPath, func(path string, info os.FileInfo, err error) error {
		ext := filepath.Ext(path)
		if info != nil && !info.IsDir() && (ext == ".yml" || ext == ".yaml") {
			data, err := ioutil.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read ci-operator config (%v)", err)
			}

			var configSpec *api.ReleaseBuildConfiguration
			if err := yaml.Unmarshal(data, &configSpec); err != nil {
				return fmt.Errorf("failed to load ci-operator config (%v)", err)
			}

			if err := configSpec.ValidateAtRuntime(); err != nil {
				return fmt.Errorf("invalid ci-operator config: %v", err)
			}
			configs[filepath.Base(path)] = *configSpec
		}
		return nil
	})
	if err != nil {
		return err
	}
	lock.Lock()
	o.configs = configs
	lock.Unlock()
	log.Info("Configs reloaded")
	return nil
}

func reloadRegistry(o *options, lock sync.Locker) error {
	log.Debug("Reloading registry")
	// reload the registry
	references, chain, workflows, err := load.Registry(o.regPath)
	if err != nil {
		log.Errorf("Failed to load updated registry")
		return err
	}
	resolver := registry.NewResolver(references, chain, workflows)
	lock.Lock()
	o.registry = resolver
	lock.Unlock()
	log.Info("Registry reloaded")
	return nil
}

func populateWatcher(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		// We only need to watch directories as creation, deletion, and writes
		// for files in a directory trigger events for the directory
		if info != nil && info.IsDir() {
			//log.Debugf("Adding %s to watch list", path)
			err = watcher.Add(path)
			if err != nil {
				return fmt.Errorf("Failed to add watch on directory %s: %v", path, err)
			}
		}
		return nil
	})
}

func reloadWatcher(w *fsnotify.Watcher, c *coalescer, o *options) {
	for {
		select {
		case event := <-w.Events:
			//log.Debugf("Received %v event for %s", event.Op, event.Name)
			if event.Op == fsnotify.Remove {
				//log.Debugf("Removing %s from watches", event.Name)
				w.Remove(event.Name)
			}
			go c.coalesce(o)
			// add new files to be watched
			if err := populateWatcher(w, o.regPath); err != nil {
				log.Errorf("Failed to update fsnotify watchlist: %v", err)
			}
		case err := <-w.Errors:
			log.Errorf("error: %v", err)
		}
	}
}

func main() {
	opts := options{}
	flag.StringVar(&opts.regPath, "registry", "", "Path to step registry")
	flag.StringVar(&opts.configPath, "config", "", "Path to config dirs")
	reloadCycle := flag.String("cycle", "", "cycle to reload registry and config files on")
	logLevel := flag.String("log-level", "info", "Level at which to log output.")
	flag.Parse()
	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Fatalf("invalid --log-level: %v", err)
	}
	log.SetLevel(level)

	registryC := coalescer{name: "registry", loader: sync.NewCond(&sync.Mutex{}), reloadFunc: reloadRegistry}
	registryC.coalesce(&opts)
	configC := coalescer{name: "configs", loader: sync.NewCond(&sync.Mutex{}), reloadFunc: buildFilenameToConfig}
	configC.coalesce(&opts)

	// While we reload immediately on file changes via fsnotify, it is possible
	// that a file may be updated while a reload is happening so reloads must
	// be run on a periodic cycle to make sure the registry is correct and up to date
	if reloadCycle != nil && *reloadCycle != "" {
		duration, err := time.ParseDuration(*reloadCycle)
		if err != nil {
			log.Fatalf("invalid cycle duration: %s", *reloadCycle)
		}
		go func() {
			for {
				time.Sleep(duration)
				registryC.coalesce(&opts)
				configC.coalesce(&opts)
			}
		}()
	}

	// watch for changes in registry directories
	registryWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create new watcher: %v", err)
	}
	defer registryWatcher.Close()
	go reloadWatcher(registryWatcher, &registryC, &opts)
	populateWatcher(registryWatcher, opts.regPath)

	// watch for changes in config directories
	configWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create new watcher: %v", err)
	}
	defer configWatcher.Close()
	go reloadWatcher(configWatcher, &configC, &opts)
	populateWatcher(configWatcher, opts.configPath)

	http.HandleFunc("/config", resolveConfig(&opts))
	http.ListenAndServe(":8080", nil)
}
