package quay_io_ci_images_distributor

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	mirroringHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: strings.ToLower(ControllerName),
			Name:      "image_mirroring_duration_seconds",
			Help:      "Image mirroring duration in seconds.",
			Buckets:   []float64{1, 2, 4, 8, 16, 32, 64},
		},
		[]string{"state"},
	)

	mirrorQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: strings.ToLower(ControllerName),
			Name:      "mirror_queue_depth",
			Help:      "Depth of mirroring queue.",
		},
		[]string{},
	)
)

// RegisterMetrics Registers metrics
func RegisterMetrics() error {
	if err := metrics.Registry.Register(mirroringHistogram); err != nil {
		return fmt.Errorf("failed to register mirroringHistogram metric: %w", err)
	}
	if err := metrics.Registry.Register(mirrorQueueDepth); err != nil {
		return fmt.Errorf("failed to register mirrorQueueDepth metric: %w", err)
	}
	return nil
}

// ObserveMirroringDuration observe the mirroring duration
func ObserveMirroringDuration(state string, value float64) {
	mirroringHistogram.WithLabelValues(state).Observe(value)
}

// SetMirrorQueueDepth sets the mirroring mirror queue's depth
func SetMirrorQueueDepth(value float64) {
	mirrorQueueDepth.WithLabelValues().Set(value)
}
