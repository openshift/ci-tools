package load

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/coalescer"
	"github.com/openshift/ci-tools/pkg/config"
)

// ConfigAgent is an interface that can load configs from disk into
// memory and retrieve them when provided with a config.Info.
type ConfigAgent interface {
	GetConfig(config.Info) (api.ReleaseBuildConfiguration, error)
	GetGeneration() int
}

type filenameToConfig map[string]api.ReleaseBuildConfiguration

type agent struct {
	lock         *sync.RWMutex
	configs      filenameToConfig
	configPath   string
	cycle        time.Duration
	generation   int
	errorMetrics *prometheus.CounterVec
}

var reloadTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "configresolver_config_reload_duration_seconds",
		Help:    "config reload duration in seconds",
		Buckets: []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1},
	},
)

func init() {
	prometheus.MustRegister(reloadTimeMetric)
}

// NewConfigAgent returns a ConfigAgent interface that automatically reloads when
// configs are changed on disk as well as on a period specified with a time.Duration.
func NewConfigAgent(configPath string, cycle time.Duration, errorMetrics *prometheus.CounterVec) (ConfigAgent, error) {
	a := &agent{configPath: configPath, cycle: cycle, lock: &sync.RWMutex{}, errorMetrics: errorMetrics}
	configCoalescer := coalescer.NewCoalescer(a.loadFilenameToConfig)
	err := configCoalescer.Run()
	if err != nil {
		return nil, fmt.Errorf("Failed to load configs: %v", err)
	}

	// periodic reload
	interrupts.TickLiteral(func() {
		if err := configCoalescer.Run(); err != nil {
			log.WithError(err).Error("Failed to reload configs")
		}
	}, a.cycle)

	// fsnotify reload
	configWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("Failed to create new watcher: %v", err)
	}
	err = populateWatcher(configWatcher, a.configPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to populate watcher: %v", err)
	}
	interrupts.Run(func(ctx context.Context) {
		reloadWatcher(ctx, configWatcher, a, configCoalescer, a.configPath)
	})
	return a, nil
}

func (a *agent) recordError(label string) {
	labels := prometheus.Labels{"error": label}
	a.errorMetrics.With(labels).Inc()
}

func (a *agent) GetConfig(info config.Info) (api.ReleaseBuildConfiguration, error) {
	a.lock.RLock()
	config, ok := a.configs[info.Basename()]
	a.lock.RUnlock()
	if !ok {
		return api.ReleaseBuildConfiguration{}, fmt.Errorf("Could not find config %s", info.Basename())
	}
	return config, nil
}

func (a *agent) GetGeneration() int {
	var gen int
	a.lock.RLock()
	gen = a.generation
	a.lock.RUnlock()
	return gen
}

// loadFilenameToConfig generates a new filenameToConfig map.
func (a *agent) loadFilenameToConfig() error {
	log.Debug("Reloading configs")
	startTime := time.Now()
	configs := filenameToConfig{}
	err := filepath.Walk(a.configPath, func(path string, info os.FileInfo, err error) error {
		ext := filepath.Ext(path)
		if info != nil && !info.IsDir() && (ext == ".yml" || ext == ".yaml") {
			configSpec, err := Config(path)
			if err != nil {
				a.recordError("failed to load config")
				return fmt.Errorf("failed to load ci-operator config (%v)", err)
			}

			if err := configSpec.ValidateAtRuntime(); err != nil {
				a.recordError("invalid config")
				return fmt.Errorf("invalid ci-operator config: %v", err)
			}
			log.Debugf("Adding %s to filenameToConfig", filepath.Base(path))
			configs[filepath.Base(path)] = *configSpec
		}
		return nil
	})
	if err != nil {
		return err
	}
	a.lock.Lock()
	a.configs = configs
	a.generation++
	a.lock.Unlock()
	duration := time.Since(startTime)
	reloadTimeMetric.Observe(float64(duration.Seconds()))
	log.WithField("duration", duration).Info("Configs reloaded")
	return nil
}

func populateWatcher(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		// We only need to watch directories as creation, deletion, and writes
		// for files in a directory trigger events for the directory
		if info != nil && info.IsDir() {
			log.Debugf("Adding %s to watch list", path)
			err = watcher.Add(path)
			if err != nil {
				return fmt.Errorf("Failed to add watch on directory %s: %v", path, err)
			}
		}
		return nil
	})
}

func reloadWatcher(ctx context.Context, w *fsnotify.Watcher, a *agent, c coalescer.Coalescer, path string) {
	for {
		select {
		case <-ctx.Done():
			if err := w.Close(); err != nil {
				log.WithError(err).Error("Failed to close fsnotify watcher")
			}
			return
		case event := <-w.Events:
			log.Debugf("Received %v event for %s", event.Op, event.Name)
			// Remove deleted files from watches
			if event.Op == fsnotify.Remove {
				log.Debugf("Removing %s from watches", event.Name)
				if err := w.Remove(event.Name); err != nil {
					a.recordError("failed to remove item from watcher")
					log.WithError(err).Errorf("Failed to remove %s from watches", event.Name)
				}
			}
			go c.Run()
			// add new files to be watched; if a watch already exists on a file, the
			// watch is simply updated
			if err := populateWatcher(w, path); err != nil {
				a.recordError("failed to update watcher")
				log.WithError(err).Error("Failed to update fsnotify watchlist")
			}
		case err := <-w.Errors:
			a.recordError("received fsnotify error")
			log.WithError(err).Errorf("Received fsnotify error")
		}
	}
}
