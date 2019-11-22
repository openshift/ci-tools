package load

import (
	"fmt"
	"sync"
	"time"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/coalescer"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/interrupts"
)

// RegistryAgent is an interface that can load a registry from disk into
// memory and resolve ReleaseBuildConfigurations using the registry
type RegistryAgent interface {
	ResolveConfig(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error)
	GetGeneration() int
}

type registryAgent struct {
	lock         *sync.RWMutex
	resolver     registry.Resolver
	registryPath string
	cycle        time.Duration
	generation   int
	errorMetrics *prometheus.CounterVec
	flatRegistry bool
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
// the registry is changed on disk as well as on a period specified with a time.Duration.
func NewRegistryAgent(registryPath string, cycle time.Duration, errorMetrics *prometheus.CounterVec, flatRegistry bool) (RegistryAgent, error) {
	a := &registryAgent{registryPath: registryPath, cycle: cycle, lock: &sync.RWMutex{}, errorMetrics: errorMetrics, flatRegistry: flatRegistry}
	registryCoalescer := coalescer.NewCoalescer(a.loadRegistry)
	err := registryCoalescer.Run()
	if err != nil {
		return nil, fmt.Errorf("Failed to load registry: %v", err)
	}

	// periodic reload
	interrupts.TickLiteral(func() {
		if err := registryCoalescer.Run(); err != nil {
			log.WithError(err).Error("Failed to reload registry")
		}
	}, a.cycle)

	err = startWatchers(a.registryPath, registryCoalescer, a.recordError)
	return a, err
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
	var gen int
	a.lock.RLock()
	gen = a.generation
	a.lock.RUnlock()
	return gen
}

func (a *registryAgent) loadRegistry() error {
	log.Debug("Reloading registry")
	startTime := time.Now()
	refs, chains, workflows, err := Registry(a.registryPath, a.flatRegistry)
	if err != nil {
		a.recordError("failed to load ci-operator registry")
		return fmt.Errorf("failed to load ci-operator registry (%v)", err)
	}
	a.lock.Lock()
	a.resolver = registry.NewResolver(refs, chains, workflows)
	a.generation++
	a.lock.Unlock()
	duration := time.Since(startTime)
	configReloadTimeMetric.Observe(float64(duration.Seconds()))
	log.WithField("duration", duration).Info("Registry reloaded")
	return nil
}
