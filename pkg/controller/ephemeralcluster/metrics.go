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

func newCountGaugeVec() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "count",
		Help:      "The number of ephemeralclusters that currently exist",
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
	}, []string{"workflow"})
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
	}, []string{"workflow"})
}

type metricsGatherer struct {
	logger                             *logrus.Entry
	client                             ctrlruntimeclient.Client
	countGauge                         *prometheus.GaugeVec
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

	provisioningDurationHistogram := newProvisioningDurationHistogramVec()
	if err := metrics.Registry.Register(provisioningDurationHistogram); err != nil {
		return nil, fmt.Errorf("failed to register provisioning duration histogram: %w", err)
	}

	deprovisioningDurationHistogram := newDeprovisioningDurationHistogramVec()
	if err := metrics.Registry.Register(deprovisioningDurationHistogram); err != nil {
		return nil, fmt.Errorf("failed to register deprovisioning duration histogram: %w", err)
	}

	metricsGatherer := newMetricsGatherer(logger.WithField("controller", "ephemeral_cluster_metrics"),
		mgr.GetClient(), countGauge, provisioningDurationHistogram, deprovisioningDurationHistogram, ecNS, interval)
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

		k := struct {
			konfluxCluster string
			konfluxTenant  string
			clusterProfile string
			workflow       string
			phase          string
		}{
			konfluxCluster: ec.KonfluxCluster(),
			konfluxTenant:  ec.KonfluxTenant(),
			clusterProfile: ec.Spec.CIOperator.Test.ClusterProfile,
			workflow:       ec.Spec.CIOperator.Test.Workflow,
			phase:          string(ec.Status.Phase),
		}

		// This combination of workflow and env var is used mainly by Konflux users.
		if hostedMgmt, ok := ec.Spec.CIOperator.Test.Env[hostedManagementClusterEnvVar]; ok && k.workflow == hypershiftHostedClusterWorkflow {
			k.workflow = k.workflow + "_" + hostedMgmt
		}

		count[k]++
	}

	mg.countGauge.Reset()
	for k, v := range count {
		mg.countGauge.
			WithLabelValues(k.konfluxCluster, k.konfluxTenant, k.clusterProfile, k.workflow, k.phase).
			Set(float64(v))
	}
}

func (mg *metricsGatherer) collectProvisioningDuration(ec *ephemeralclusterv1.EphemeralCluster, status *ephemeralclusterv1.EphemeralClusterStatus) bool {
	find := func(t ephemeralclusterv1.EphemeralClusterConditionType) (time.Time, bool) {
		for i := range status.Conditions {
			if c := &status.Conditions[i]; c.Type == t && c.Status == ephemeralclusterv1.ConditionTrue {
				return c.LastTransitionTime.Time, true
			}
		}
		return time.Time{}, false
	}

	if end, ok := find(ephemeralclusterv1.ClusterReady); ok {
		start := ec.CreationTimestamp.Time
		duration := end.Sub(start).Seconds()
		workflow := ec.Spec.CIOperator.Test.Workflow
		mg.provisioningDurationHistogramVec.WithLabelValues(workflow).Observe(duration)
		return true
	}

	return false
}

func (mg *metricsGatherer) collectDeprovisioningDuration(ec *ephemeralclusterv1.EphemeralCluster, oldStatus, observedStatus *ephemeralclusterv1.EphemeralClusterStatus) bool {
	find := func(status *ephemeralclusterv1.EphemeralClusterStatus, t ephemeralclusterv1.EphemeralClusterConditionType) (time.Time, bool) {
		for i := range status.Conditions {
			if c := &status.Conditions[i]; c.Type == t && c.Status == ephemeralclusterv1.ConditionTrue {
				return c.LastTransitionTime.Time, true
			}
		}
		return time.Time{}, false
	}

	start, startOk := find(oldStatus, ephemeralclusterv1.TestCompleted)
	end, endOk := find(observedStatus, ephemeralclusterv1.ProwJobCompleted)
	if startOk && endOk {
		duration := end.Sub(start).Seconds()
		workflow := ec.Spec.CIOperator.Test.Workflow
		mg.deprovisioningDurationHistogramVec.WithLabelValues(workflow).Observe(duration)
		return true
	}

	return false
}

func (mg *metricsGatherer) NeedLeaderElection() bool { return true }

func newMetricsGatherer(logger *logrus.Entry, client ctrlruntimeclient.Client,
	countGauge *prometheus.GaugeVec, provisioningDurationHistogramVec, deprovisioningDurationHistogramVec *prometheus.HistogramVec,
	ecNS string, interval time.Duration) *metricsGatherer {
	return &metricsGatherer{
		logger:                             logger,
		client:                             client,
		countGauge:                         countGauge,
		provisioningDurationHistogramVec:   provisioningDurationHistogramVec,
		deprovisioningDurationHistogramVec: deprovisioningDurationHistogramVec,
		ecNS:                               ecNS,
		interval:                           interval,
	}
}
