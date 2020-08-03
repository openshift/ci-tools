package agents

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
)

// RegistryAgent is an interface that can load a registry from disk into
// memory and resolve ReleaseBuildConfigurations using the registry
type RegistryAgent interface {
	ResolveConfig(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error)
	GetRegistryComponents() (registry.ReferenceByName, registry.ChainByName, registry.WorkflowByName, map[string]string, *api.RegistryMetadata)
	GetGeneration() int
	registry.Resolver
}

type registryAgent struct {
	lock          *sync.RWMutex
	resolver      registry.Resolver
	registryPath  string
	generation    int
	errorMetrics  *prometheus.CounterVec
	flatRegistry  bool
	references    registry.ReferenceByName
	chains        registry.ChainByName
	workflows     registry.WorkflowByName
	documentation map[string]string
	metadata      *api.RegistryMetadata
}

var registryReloadTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "configresolver_registry_reload_duration_seconds",
		Help:    "registry reload duration in seconds",
		Buckets: []float64{0.5, 0.75, 1, 1.25, 1.5, 2, 2.5, 3, 4, 5, 6},
	},
)

func init() {
	prometheus.MustRegister(registryReloadTimeMetric)
}

// NewRegistryAgent returns a RegistryAgent interface that automatically reloads when
// the registry is changed on disk.
func NewRegistryAgent(registryPath string, errorMetrics *prometheus.CounterVec, flatRegistry bool) (RegistryAgent, error) {
	a := &registryAgent{registryPath: registryPath, lock: &sync.RWMutex{}, errorMetrics: errorMetrics, flatRegistry: flatRegistry}
	// Load config once so we fail early if that doesn't work and are ready as soon as we return
	if err := a.loadRegistry(); err != nil {
		return nil, fmt.Errorf("failed to load registry: %w", err)
	}

	return a, startWatchers(a.registryPath, a.loadRegistry, a.recordError)
}

func (a *registryAgent) recordError(label string) {
	labels := prometheus.Labels{"error": label}
	a.errorMetrics.With(labels).Inc()
}

// ResolveConfig uses the registryAgent's resolver to resolve a provided ReleaseBuildConfiguration
func (a *registryAgent) ResolveConfig(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error) {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return registry.ResolveConfig(a.resolver, config)
}

func (a *registryAgent) GetGeneration() int {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return a.generation
}

func (a *registryAgent) GetRegistryComponents() (registry.ReferenceByName, registry.ChainByName, registry.WorkflowByName, map[string]string, *api.RegistryMetadata) {
	return a.references, a.chains, a.workflows, a.documentation, a.metadata
}

func (a *registryAgent) loadRegistry() error {
	logrus.Debug("Reloading registry")
	startTime := time.Now()
	references, chains, workflows, documentation, metadata, err := load.Registry(a.registryPath, a.flatRegistry)
	if err != nil {
		a.recordError("failed to load ci-operator registry")
		return fmt.Errorf("failed to load ci-operator registry (%w)", err)
	}
	a.lock.Lock()
	a.references = references
	a.chains = chains
	a.workflows = workflows
	a.documentation = documentation
	a.metadata = metadata
	a.resolver = registry.NewResolver(references, chains, workflows)
	a.generation++
	a.lock.Unlock()
	duration := time.Since(startTime)
	configReloadTimeMetric.Observe(duration.Seconds())
	logrus.WithField("duration", duration).Info("Registry reloaded")
	return nil
}

func (a *registryAgent) Resolve(name string, config api.MultiStageTestConfiguration) (api.MultiStageTestConfigurationLiteral, error) {
	return a.resolver.Resolve(name, config)
}
