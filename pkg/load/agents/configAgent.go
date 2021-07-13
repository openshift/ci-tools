package agents

import (
	"fmt"
	"reflect"
	"regexp"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
)

type IndexDelta struct {
	IndexKey string
	Added    []*api.ReleaseBuildConfiguration
	Removed  []*api.ReleaseBuildConfiguration
}

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
	// SubscribeToIndexChanges return a channel with all changes to the given index. The index
	// does not have to exist yet. If you want to get the initial set of changes, subscribe
	// before the index exists.
	SubscribeToIndexChanges(indexName string) (<-chan IndexDelta, error)
}

// IndexFn can be used to add indexes to the ConfigAgent
type IndexFn func(api.ReleaseBuildConfiguration) []string

type configAgent struct {
	lock             *sync.RWMutex
	configs          load.ByOrgRepo
	configPath       string
	generation       int
	errorMetrics     *prometheus.CounterVec
	indexFuncs       map[string]IndexFn
	indexes          map[string]configIndex
	indexSubscribers map[string][]chan IndexDelta
	reloadConfig     func() error
	// closeIndexDeltaSubscriberChannelAfterFirstIndexBuild is used in testing to allow
	// tests to wait for all deltas associated to an index build.
	closeIndexDeltaSubscriberChannelAfterFirstIndexBuild bool
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
		a.lock.Lock()
		defer a.lock.Unlock()
		a.buildIndexes()
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

func (a *configAgent) SubscribeToIndexChanges(indexName string) (<-chan IndexDelta, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.indexSubscribers == nil {
		a.indexSubscribers = map[string][]chan IndexDelta{}
	}
	newChan := make(chan IndexDelta)
	a.indexSubscribers[indexName] = append(a.indexSubscribers[indexName], newChan)
	return newChan, nil
}

// loadFilenameToConfig generates a new filenameToConfig map.
func (a *configAgent) loadFilenameToConfig() error {
	logrus.Debug("Reloading configs")
	duration, err := func() (time.Duration, error) {
		a.lock.Lock()
		defer a.lock.Unlock()
		startTime := time.Now()
		configs, err := load.FromPathByOrgRepo(a.configPath)
		if err != nil {
			return time.Duration(0), fmt.Errorf("loading config failed: %w", err)
		}
		a.configs = configs
		a.buildIndexes()
		a.generation++
		return time.Since(startTime), nil
	}()
	if err != nil {
		return err
	}
	configReloadTimeMetric.Observe(duration.Seconds())
	logrus.WithField("duration", duration.String()).Info("Configs reloaded")
	return nil
}

func (a *configAgent) buildIndexes() {
	oldIndexes := a.indexes

	a.indexes = map[string]configIndex{}
	for indexName, indexFunc := range a.indexFuncs {
		// Make sure the index always exists even if empty, otherwise we return a confusing
		// "index does not exist error" in case its empty
		if _, exists := a.indexes[indexName]; !exists {
			a.indexes[indexName] = configIndex{}
		}
		for _, orgConfigs := range a.configs {
			for _, repoConfigs := range orgConfigs {
				for _, config := range repoConfigs {
					var resusableConfigPtr *api.ReleaseBuildConfiguration

					for _, indexKey := range indexFunc(config) {
						if resusableConfigPtr == nil {
							config := config
							resusableConfigPtr = &config
						}
						if _, exists := a.indexes[indexName][indexKey]; !exists {
							a.indexes[indexName][indexKey] = []*api.ReleaseBuildConfiguration{}
						}

						a.indexes[indexName][indexKey] = append(a.indexes[indexName][indexKey], resusableConfigPtr)
					}
				}
			}
		}

		// Building the diff is expensive, so cache it in case we have multiple
		// subscribers.
		var changes []IndexDelta
		for _, channel := range a.indexSubscribers[indexName] {
			if changes == nil {
				changes = buildIndexDelta(oldIndexes[indexName], a.indexes[indexName])
			}

			// This might block, so do it in a new goroutine
			channel := channel
			go func() {
				for _, change := range changes {
					channel <- change
				}
				if a.closeIndexDeltaSubscriberChannelAfterFirstIndexBuild {
					close(channel)
				}
			}()
		}
	}
}

// configByFilenameFromIndex constructs a map indexKey -> Metadata -> config for the config
// in the provided index. It uses the fact that Metadata is unique per config to speed up
// looking up specific values in an index.
func configByFilenameFromIndex(index configIndex) map[string]map[api.Metadata]*api.ReleaseBuildConfiguration {
	result := make(map[string]map[api.Metadata]*api.ReleaseBuildConfiguration, len(index))
	for indexKey, indexValues := range index {
		if _, ok := result[indexKey]; !ok {
			result[indexKey] = make(map[api.Metadata]*api.ReleaseBuildConfiguration, len(indexValues))
		}
		for idx, config := range indexValues {
			result[indexKey][config.Metadata] = indexValues[idx]
		}
	}

	return result
}

func buildIndexDelta(oldIndex, newIndex configIndex) []IndexDelta {
	changesByKey := map[string]*IndexDelta{}

	oldIndexesMappedByMetadata := configByFilenameFromIndex(oldIndex)
	newIndexedMappedByMetadata := configByFilenameFromIndex(newIndex)

	alreadyProcessed := map[string]map[api.Metadata]struct{}{}

	for indexKey, configMap := range oldIndexesMappedByMetadata {
		alreadyProcessed[indexKey] = map[api.Metadata]struct{}{}
		for key, value := range configMap {
			alreadyProcessed[indexKey][key] = struct{}{}
			if reflect.DeepEqual(newIndexedMappedByMetadata[indexKey][key], value) {
				continue
			}
			if changesByKey[indexKey] == nil {
				changesByKey[indexKey] = &IndexDelta{IndexKey: indexKey}
			}
			changesByKey[indexKey].Removed = append(changesByKey[indexKey].Removed, configMap[key])
			if newValue := newIndexedMappedByMetadata[indexKey][key]; newValue != nil {
				changesByKey[indexKey].Added = append(changesByKey[indexKey].Added, newValue)
			}
		}
	}
	for indexKey, configMap := range newIndexedMappedByMetadata {
		for key, value := range configMap {
			if _, ok := alreadyProcessed[indexKey][key]; ok {
				continue
			}
			if reflect.DeepEqual(oldIndexesMappedByMetadata[indexKey][key], value) {
				continue
			}
			if changesByKey[indexKey] == nil {
				changesByKey[indexKey] = &IndexDelta{IndexKey: indexKey}
			}
			changesByKey[indexKey].Added = append(changesByKey[indexKey].Added, configMap[key])
			if oldValue := oldIndexesMappedByMetadata[indexKey][key]; oldValue != nil {
				changesByKey[indexKey].Removed = append(changesByKey[indexKey].Removed, oldValue)
			}
		}
	}

	var result []IndexDelta
	for _, value := range changesByKey {
		result = append(result, *value)
	}

	return result
}
