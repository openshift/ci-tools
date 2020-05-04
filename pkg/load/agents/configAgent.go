package agents

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/coalescer"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
)

// ConfigAgent is an interface that can load configs from disk into
// memory and retrieve them when provided with a config.Info.
type ConfigAgent interface {
	GetConfig(config.Info) (api.ReleaseBuildConfiguration, error)
	GetAll() load.FilenameToConfig
	GetGeneration() int
	AddIndex(indexName string, indexFunc IndexFn) error
	GetFromIndex(indexName string, indexKey string) ([]*api.ReleaseBuildConfiguration, error)
}

// IndexFn can be used to add indexes to the ConfigAgent
type IndexFn func(api.ReleaseBuildConfiguration) []string

type configAgent struct {
	lock         *sync.RWMutex
	configs      load.FilenameToConfig
	configPath   string
	cycle        time.Duration
	generation   int
	errorMetrics *prometheus.CounterVec
	indexFuncs   map[string]IndexFn
	indexes      map[string]configIndex
}

type configIndex map[string][]*api.ReleaseBuildConfiguration

var configReloadTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "configresolver_config_reload_duration_seconds",
		Help:    "config reload duration in seconds",
		Buckets: []float64{0.5, 0.75, 1, 1.25, 1.5, 2, 2.5, 3, 4, 5, 6},
	},
)

func init() {
	prometheus.MustRegister(configReloadTimeMetric)
}

// NewConfigAgent returns a ConfigAgent interface that automatically reloads when
// configs are changed on disk as well as on a period specified with a time.Duration.
func NewConfigAgent(configPath string, cycle time.Duration, errorMetrics *prometheus.CounterVec) (ConfigAgent, error) {
	a := &configAgent{configPath: configPath, cycle: cycle, lock: &sync.RWMutex{}, errorMetrics: errorMetrics}
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

	err = startWatchers(a.configPath, configCoalescer, a.recordError)
	return a, err
}

func (a *configAgent) recordError(label string) {
	labels := prometheus.Labels{"error": label}
	a.errorMetrics.With(labels).Inc()
}

func (a *configAgent) GetConfig(info config.Info) (api.ReleaseBuildConfiguration, error) {
	a.lock.RLock()
	defer a.lock.RUnlock()
	config, ok := a.configs[info.Basename()]
	if !ok {
		return api.ReleaseBuildConfiguration{}, fmt.Errorf("Could not find config %s", info.Basename())
	}
	return config, nil
}

func (a *configAgent) GetAll() load.FilenameToConfig {
	return a.configs
}

func (a *configAgent) GetGeneration() int {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return a.generation
}

func (a *configAgent) GetFromIndex(indexName string, indexKey string) ([]*api.ReleaseBuildConfiguration, error) {
	a.lock.RLock()
	defer a.lock.RUnlock()
	if _, exists := a.indexes[indexName]; !exists {
		return nil, fmt.Errorf("no index %s configured", indexName)
	}
	return a.indexes[indexName][indexKey], nil
}

func (a *configAgent) AddIndex(indexName string, indexFunc IndexFn) error {
	a.lock.Lock()
	defer a.lock.Unlock()
	if a.indexFuncs == nil {
		a.indexFuncs = map[string]IndexFn{}
	}
	if _, exists := a.indexFuncs[indexName]; exists {
		return fmt.Errorf("there is already an index named %q", indexName)
	}
	a.indexFuncs[indexName] = indexFunc
	return nil
}

// loadFilenameToConfig generates a new filenameToConfig map.
func (a *configAgent) loadFilenameToConfig() error {
	log.Debug("Reloading configs")
	startTime := time.Now()
	configs, err := load.FromPath(a.configPath)
	if err != nil {
		return fmt.Errorf("loading config failed: %w", err)
	}

	indexes := a.buildIndexes(configs)

	a.lock.Lock()
	a.configs = configs
	a.generation++
	a.indexes = indexes
	a.lock.Unlock()
	duration := time.Since(startTime)
	configReloadTimeMetric.Observe(float64(duration.Seconds()))
	log.WithField("duration", duration).Info("Configs reloaded")
	return nil
}

func (a *configAgent) buildIndexes(configs load.FilenameToConfig) map[string]configIndex {
	indexes := map[string]configIndex{}
	for indexName, indexFunc := range a.indexFuncs {
		for _, config := range configs {
			var resusableConfigPtr *api.ReleaseBuildConfiguration

			for _, indexKey := range indexFunc(config) {
				if _, exists := indexes[indexName]; !exists {
					indexes[indexName] = configIndex{}
				}
				if resusableConfigPtr == nil {
					config := config
					resusableConfigPtr = &config
				}
				if _, exists := indexes[indexName][indexKey]; !exists {
					indexes[indexName][indexKey] = []*api.ReleaseBuildConfiguration{}
				}

				indexes[indexName][indexKey] = append(indexes[indexName][indexKey], resusableConfigPtr)
			}
		}
	}

	return indexes
}
