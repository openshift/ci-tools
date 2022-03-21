package agents

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	utilpointer "k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
)

// RegistryAgent is an interface that can load a registry from disk into
// memory and resolve ReleaseBuildConfigurations using the registry
type RegistryAgent interface {
	ResolveConfig(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error)
	GetRegistryComponents() (registry.ReferenceByName, registry.ChainByName, registry.WorkflowByName, map[string]string, api.RegistryMetadata)
	GetGeneration() int
	registry.Resolver
}

type registryAgent struct {
	lock          *sync.RWMutex
	resolver      registry.Resolver
	registryPath  string
	generation    int
	errorMetrics  *prometheus.CounterVec
	flags         load.RegistryFlag
	references    registry.ReferenceByName
	chains        registry.ChainByName
	workflows     registry.WorkflowByName
	documentation map[string]string
	metadata      api.RegistryMetadata
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

type RegistryAgentOptions struct {
	// ErrorMetric holds the CounterVec to count errors on. It must include a `error` label
	// or the agent panics on the first error.
	ErrorMetric *prometheus.CounterVec
	// FlatRegistry describes if the registry is flat, which means org/repo/branch info can not be inferred
	// from the filepath. Defaults to true.
	FlatRegistry *bool
}

type RegistryAgentOption func(*RegistryAgentOptions)

func WithRegistryMetrics(m *prometheus.CounterVec) RegistryAgentOption {
	return func(o *RegistryAgentOptions) {
		o.ErrorMetric = m
	}
}

func WithRegistryFlat(v bool) RegistryAgentOption {
	return func(o *RegistryAgentOptions) {
		o.FlatRegistry = &v
	}
}

// NewRegistryAgent returns a RegistryAgent interface that automatically reloads when
// the registry is changed on disk.
func NewRegistryAgent(registryPath string, opts ...RegistryAgentOption) (RegistryAgent, error) {
	opt := &RegistryAgentOptions{}
	for _, o := range opts {
		o(opt)
	}
	if opt.ErrorMetric == nil {
		opt.ErrorMetric = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "registry_agent_errors_total"}, []string{"error"})
	}
	if opt.FlatRegistry == nil {
		opt.FlatRegistry = utilpointer.BoolPtr(true)
	}
	flags := load.RegistryMetadata | load.RegistryDocumentation
	if *opt.FlatRegistry {
		flags |= load.RegistryFlat
	}
	a := &registryAgent{
		registryPath: registryPath,
		lock:         &sync.RWMutex{},
		errorMetrics: opt.ErrorMetric,
		flags:        flags,
	}
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

func (a *registryAgent) GetRegistryComponents() (registry.ReferenceByName, registry.ChainByName, registry.WorkflowByName, map[string]string, api.RegistryMetadata) {
	return a.references, a.chains, a.workflows, a.documentation, a.metadata
}

func (a *registryAgent) loadRegistry() error {
	logrus.Debug("Reloading registry")
	duration, err := func() (time.Duration, error) {
		a.lock.Lock()
		defer a.lock.Unlock()
		startTime := time.Now()
		references, chains, workflows, documentation, metadata, observers, err := load.Registry(a.registryPath, a.flags)
		if err != nil {
			a.recordError("failed to load ci-operator registry")
			return time.Duration(0), fmt.Errorf("failed to load ci-operator registry (%w)", err)
		}
		a.references = references
		a.chains = chains
		a.workflows = workflows
		a.documentation = documentation
		a.metadata = metadata
		a.resolver = registry.NewResolver(references, chains, workflows, observers)
		a.generation++
		return time.Since(startTime), nil
	}()
	if err != nil {
		return err
	}
	registryReloadTimeMetric.Observe(duration.Seconds())
	logrus.WithField("duration", duration).Info("Registry reloaded")
	return nil
}

func (a *registryAgent) Resolve(name string, config api.MultiStageTestConfiguration) (api.MultiStageTestConfigurationLiteral, error) {
	return a.resolver.Resolve(name, config)
}
