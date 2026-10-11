package ephemeralcluster

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
)

const (
	metricsNamespace = "ephemeralcluster"

	hostedManagementClusterEnvVar   = "HOSTED_MANAGEMENT_CLUSTER"
	hypershiftHostedClusterWorkflow = "hypershift-hostedcluster-workflow"
)

func workflowLabel(ec *ephemeralclusterv1.EphemeralCluster) string {
	workflow := ec.Spec.CIOperator.Test.Workflow
	if hostedMgmt, ok := ec.Spec.CIOperator.Test.Env[hostedManagementClusterEnvVar]; ok && workflow == hypershiftHostedClusterWorkflow {
		workflow = workflow + "_" + hostedMgmt
	}
	return workflow
}

func newCountGaugeVec() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "count",
		Help:      "The number of ephemeralclusters that currently exist",
	}, []string{"konflux_cluster", "konflux_tenant", "cluster_profile", "workflow", "phase"})
}

func newPhaseTransitionsCounterVec() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "phase_transitions_total",
		Help: "This counter keeps track of transitions between cluster's phases. " +
			"The provisioning outcome is reliable, the deprovisioning one is a lower bound: a workflow is free to " +
			"run its teardown as a best effort post step, in which case the ProwJob succeeds no matter what happened",
	}, []string{"konflux_cluster", "konflux_tenant", "cluster_profile", "workflow", "phase"})
}

var (
	provisioningDurationBuckets = []float64{300, 600, 900, 1800, 3600, 5400, 7200, 9000, 10800}
)

func newProvisioningDurationHistogramVec() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "provisioning_duration_seconds",
		Help:      "Measure how long the provisioning procedure takes",
		Buckets:   provisioningDurationBuckets,
	}, []string{"konflux_cluster", "konflux_tenant", "cluster_profile", "workflow", "phase"})
}

var (
	deprovisioningDurationBuckets = []float64{300, 600, 900, 1800, 3600, 5400, 7200, 9000, 10800}
)

func newDeprovisioningDurationHistogramVec() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "deprovisioning_duration_seconds",
		Help:      "Measure how long the deprovisioning procedure takes",
		Buckets:   deprovisioningDurationBuckets,
	}, []string{"konflux_cluster", "konflux_tenant", "cluster_profile", "workflow", "phase"})
}

type metricsGatherer struct {
	logger                             *logrus.Entry
	client                             ctrlruntimeclient.Client
	countGauge                         *prometheus.GaugeVec
	phaseTransitionsCounter            *prometheus.CounterVec
	provisioningDurationHistogramVec   *prometheus.HistogramVec
	deprovisioningDurationHistogramVec *prometheus.HistogramVec
	ecNS                               string
	interval                           time.Duration
}

func addMetricsToManager(logger *logrus.Entry, mgr manager.Manager, ecNS string, interval time.Duration) (*metricsGatherer, error) {
	countGauge := newCountGaugeVec()
	if err := metrics.Registry.Register(countGauge); err != nil {
		return nil, fmt.Errorf("failed to register count gauge: %w", err)
	}

	phaseOutcomeCounter := newPhaseTransitionsCounterVec()
	if err := metrics.Registry.Register(phaseOutcomeCounter); err != nil {
		return nil, fmt.Errorf("failed to register phase_outcome_total counter: %w", err)
	}

	provisioningDurationHistogram := newProvisioningDurationHistogramVec()
	if err := metrics.Registry.Register(provisioningDurationHistogram); err != nil {
		return nil, fmt.Errorf("failed to register provisioning duration histogram: %w", err)
	}

	deprovisioningDurationHistogram := newDeprovisioningDurationHistogramVec()
	if err := metrics.Registry.Register(deprovisioningDurationHistogram); err != nil {
		return nil, fmt.Errorf("failed to register deprovisioning duration histogram: %w", err)
	}

	metricsGatherer := newMetricsGatherer(logger.WithField("controller", "ephemeral_cluster_metrics"),
		mgr.GetClient(), countGauge, phaseOutcomeCounter,
		provisioningDurationHistogram, deprovisioningDurationHistogram, ecNS, interval)
	if err := mgr.Add(metricsGatherer); err != nil {
		return nil, fmt.Errorf("add metrics to manager: %w", err)
	}

	return metricsGatherer, nil
}

func (mg *metricsGatherer) Start(ctx context.Context) error {
	ticker := time.NewTicker(mg.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			ecList := ephemeralclusterv1.EphemeralClusterList{}
			if err := mg.client.List(ctx, &ecList, ctrlruntimeclient.InNamespace(mg.ecNS)); err != nil {
				mg.logger.WithError(err).Error("failed to list ephemeralclusters for metrics")
				continue
			}

			mg.collectCount(&ecList)
		}
	}
}

func (mg *metricsGatherer) collectCount(ecList *ephemeralclusterv1.EphemeralClusterList) {
	count := make(map[struct {
		konfluxCluster string
		konfluxTenant  string
		clusterProfile string
		workflow       string
		phase          string
	}]uint)

	for i := range ecList.Items {
		ec := &ecList.Items[i]

		labels := struct {
			konfluxCluster string
			konfluxTenant  string
			clusterProfile string
			workflow       string
			phase          string
		}{
			konfluxCluster: ec.KonfluxCluster(),
			konfluxTenant:  ec.KonfluxTenant(),
			clusterProfile: ec.Spec.CIOperator.Test.ClusterProfile,
			workflow:       workflowLabel(ec),
			phase:          string(ec.Status.Phase),
		}

		count[labels]++
	}

	mg.countGauge.Reset()
	for k, v := range count {
		mg.countGauge.
			WithLabelValues(k.konfluxCluster, k.konfluxTenant, k.clusterProfile, k.workflow, k.phase).
			Set(float64(v))
	}
}

func (mg *metricsGatherer) collectProvisioningDuration(ec *ephemeralclusterv1.EphemeralCluster, duration float64) {
	labels := struct {
		konfluxCluster string
		konfluxTenant  string
		clusterProfile string
		workflow       string
		phase          string
	}{
		konfluxCluster: ec.KonfluxCluster(),
		konfluxTenant:  ec.KonfluxTenant(),
		clusterProfile: ec.Spec.CIOperator.Test.ClusterProfile,
		workflow:       workflowLabel(ec),
		phase:          string(ec.Status.Phase),
	}
	mg.provisioningDurationHistogramVec.
		WithLabelValues(labels.konfluxCluster, labels.konfluxTenant, labels.clusterProfile, labels.workflow, labels.phase).
		Observe(duration)
}

func (mg *metricsGatherer) collectDeprovisioningDuration(ec *ephemeralclusterv1.EphemeralCluster, duration float64) {
	labels := struct {
		konfluxCluster string
		konfluxTenant  string
		clusterProfile string
		workflow       string
		phase          string
	}{
		konfluxCluster: ec.KonfluxCluster(),
		konfluxTenant:  ec.KonfluxTenant(),
		clusterProfile: ec.Spec.CIOperator.Test.ClusterProfile,
		workflow:       workflowLabel(ec),
		phase:          string(ec.Status.Phase),
	}
	mg.deprovisioningDurationHistogramVec.
		WithLabelValues(labels.konfluxCluster, labels.konfluxTenant, labels.clusterProfile, labels.workflow, labels.phase).
		Observe(duration)
}

func (mg *metricsGatherer) collectPhaseTransitions(ec *ephemeralclusterv1.EphemeralCluster, phase string) {
	labels := struct {
		konfluxCluster string
		konfluxTenant  string
		clusterProfile string
		workflow       string
		phase          string
	}{
		konfluxCluster: ec.KonfluxCluster(),
		konfluxTenant:  ec.KonfluxTenant(),
		clusterProfile: ec.Spec.CIOperator.Test.ClusterProfile,
		workflow:       workflowLabel(ec),
		phase:          phase,
	}

	mg.phaseTransitionsCounter.
		WithLabelValues(labels.konfluxCluster, labels.konfluxTenant, labels.clusterProfile, labels.workflow, labels.phase).
		Inc()
}

func (mg *metricsGatherer) NeedLeaderElection() bool { return true }

func newMetricsGatherer(logger *logrus.Entry, client ctrlruntimeclient.Client,
	countGauge *prometheus.GaugeVec, phaseTransitionsCounter *prometheus.CounterVec,
	provisioningDurationHistogramVec, deprovisioningDurationHistogramVec *prometheus.HistogramVec,
	ecNS string, interval time.Duration) *metricsGatherer {
	return &metricsGatherer{
		logger:                             logger,
		client:                             client,
		countGauge:                         countGauge,
		phaseTransitionsCounter:            phaseTransitionsCounter,
		provisioningDurationHistogramVec:   provisioningDurationHistogramVec,
		deprovisioningDurationHistogramVec: deprovisioningDurationHistogramVec,
		ecNS:                               ecNS,
		interval:                           interval,
	}
}
