package agents

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
)

// ConfigAgent is an interface that can load configs from disk into
// memory and retrieve them when provided with a config.Info.
type ConfigAgent interface {
	// GetMatchingConfig loads a configuration that matches the metadata,
	// allowing for regex matching on branch names.
	GetMatchingConfig(metadata api.Metadata) (api.ReleaseBuildConfiguration, error)
	GetAll() load.ByOrgRepo
	GetGeneration() int
	AddIndex(indexName string, indexFunc IndexFn) error
	GetFromIndex(indexName string, indexKey string) ([]*api.ReleaseBuildConfiguration, error)
}

// IndexFn can be used to add indexes to the ConfigAgent
type IndexFn func(api.ReleaseBuildConfiguration) []string

type configAgent struct {
	lock         *sync.RWMutex
	configs      load.ByOrgRepo
	configPath   string
	generation   int
	errorMetrics *prometheus.CounterVec
	indexFuncs   map[string]IndexFn
	indexes      map[string]configIndex
	reloadConfig func() error
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

// NewFakeConfigAgent returns a new static config agent
// that can be used for tests
func NewFakeConfigAgent(configs load.ByOrgRepo) ConfigAgent {
	a := &configAgent{
		lock:         &sync.RWMutex{},
		configs:      configs,
		errorMetrics: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"error"}),
	}
	a.reloadConfig = func() error {
		indexes := a.buildIndexes(a.configs)
		a.lock.Lock()
		a.indexes = indexes
		a.lock.Unlock()
		return nil
	}

	return a
}

type ConfigAgentOptions struct {
	// ErrorMetric holds the CounterVec to count errors on. It must include a `error` label
	// or the agent panics on the first error.
	ErrorMetric *prometheus.CounterVec
}

type ConfigAgentOption func(*ConfigAgentOptions)

func WithConfigMetrics(m *prometheus.CounterVec) ConfigAgentOption {
	return func(o *ConfigAgentOptions) {
		o.ErrorMetric = m
	}
}

// NewConfigAgent returns a ConfigAgent interface that automatically reloads when
// configs are changed on disk.
func NewConfigAgent(configPath string, opts ...ConfigAgentOption) (ConfigAgent, error) {
	opt := &ConfigAgentOptions{}
	for _, o := range opts {
		o(opt)
	}
	if opt.ErrorMetric == nil {
		opt.ErrorMetric = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "config_agent_errors_total"}, []string{"error"})
	}
	a := &configAgent{configPath: configPath, lock: &sync.RWMutex{}, errorMetrics: opt.ErrorMetric}
	a.reloadConfig = a.loadFilenameToConfig
	// Load config once so we fail early if that doesn't work and are ready as soon as we return
	if err := a.reloadConfig(); err != nil {
		return nil, fmt.Errorf("failed to laod config: %w", err)
	}

	return a, startWatchers(a.configPath, a.reloadConfig, a.recordError)
}

func (a *configAgent) recordError(label string) {
	labels := prometheus.Labels{"error": label}
	a.errorMetrics.With(labels).Inc()
}

// GetMatchingConfig loads a configuration that matches the metadata,
// allowing for regex matching on branch names.
func (a *configAgent) GetMatchingConfig(metadata api.Metadata) (api.ReleaseBuildConfiguration, error) {
	a.lock.RLock()
	defer a.lock.RUnlock()
	orgConfigs, exist := a.configs[metadata.Org]
	if !exist {
		return api.ReleaseBuildConfiguration{}, fmt.Errorf("could not find any config for org %s", metadata.Org)
	}
	repoConfigs, exist := orgConfigs[metadata.Repo]
	if !exist {
		return api.ReleaseBuildConfiguration{}, fmt.Errorf("could not find any config for repo %s/%s", metadata.Org, metadata.Repo)
	}
	var matchingConfigs []api.ReleaseBuildConfiguration
	for _, config := range repoConfigs {
		r, err := regexp.Compile(config.Metadata.Branch)
		if err != nil {
			return api.ReleaseBuildConfiguration{}, fmt.Errorf("could not compile regex for %s/%s@%s: %w", metadata.Org, metadata.Repo, config.Metadata.Branch, err)
		}
		if r.MatchString(metadata.Branch) && config.Metadata.Variant == metadata.Variant {
			matchingConfigs = append(matchingConfigs, config)
		}
	}
	switch len(matchingConfigs) {
	case 0:
		return api.ReleaseBuildConfiguration{}, fmt.Errorf("could not find any config for branch %s on repo %s/%s", metadata.Branch, metadata.Org, metadata.Repo)
	case 1:
		return matchingConfigs[0], nil
	default:
		return api.ReleaseBuildConfiguration{}, fmt.Errorf("found more than one matching config for branch %s on repo %s/%s", metadata.Branch, metadata.Org, metadata.Repo)
	}
}

func (a *configAgent) GetAll() load.ByOrgRepo {
	a.lock.RLock()
	defer a.lock.RUnlock()
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
	// Closure to capture the defer statement
	if err := func() error {
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
	}(); err != nil {
		return err
	}

	// Make sure the index is available after we return
	if err := a.reloadConfig(); err != nil {
		return fmt.Errorf("failed to reload config after adding index: %w", err)
	}
	return nil
}

// loadFilenameToConfig generates a new filenameToConfig map.
func (a *configAgent) loadFilenameToConfig() error {
	a.lock.Lock()
	logrus.Debug("Reloading configs")
	startTime := time.Now()
	configs, err := load.FromPathByOrgRepo(a.configPath)
	if err != nil {
		return fmt.Errorf("loading config failed: %w", err)
	}

	indexes := a.buildIndexes(configs)
	a.configs = configs
	a.generation++
	a.indexes = indexes
	a.lock.Unlock()
	duration := time.Since(startTime)
	configReloadTimeMetric.Observe(duration.Seconds())
	logrus.WithField("duration", duration).Info("Configs reloaded")
	return nil
}

func (a *configAgent) buildIndexes(orgRepoConfigs load.ByOrgRepo) map[string]configIndex {
	indexes := map[string]configIndex{}
	for indexName, indexFunc := range a.indexFuncs {
		// Make sure the index always exists even if empty, otherwise we return a confusing
		// "index does not exist error" in case its empty
		if _, exists := indexes[indexName]; !exists {
			indexes[indexName] = configIndex{}
		}
		for _, orgConfigs := range orgRepoConfigs {
			for _, repoConfigs := range orgConfigs {
				for _, config := range repoConfigs {
					var resusableConfigPtr *api.ReleaseBuildConfiguration

					for _, indexKey := range indexFunc(config) {
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
		}
	}

	return indexes
}
