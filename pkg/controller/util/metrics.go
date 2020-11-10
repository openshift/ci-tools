package util

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	successfulImportsCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "imagestream_successful_import_count",
		Help: "The number of imagestream imports the controller created succesfull",
	}, []string{"controller", "cluster", "namespace"})

	failedImportsCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "imagestream_failed_import_count",
		Help: "The number of failed imagestream imports the controller create",
	}, []string{"controller", "cluster", "namespace"})
)

// RegisterMetrics Registers metrics
func RegisterMetrics() error {
	if err := metrics.Registry.Register(successfulImportsCounter); err != nil {
		return fmt.Errorf("failed to register successfulImportsCounter metric: %w", err)
	}
	if err := metrics.Registry.Register(failedImportsCounter); err != nil {
		return fmt.Errorf("failed to register failedImportsCounter metric: %w", err)
	}
	return nil
}

// CountImportResult increase the counter metric
func CountImportResult(controllerName, cluster, namespace string, successful bool) {
	if successful {
		successfulImportsCounter.WithLabelValues(controllerName, cluster, namespace).Inc()
	} else {
		failedImportsCounter.WithLabelValues(controllerName, cluster, namespace).Inc()
	}
}
