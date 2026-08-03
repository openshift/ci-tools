package ephemeralcluster

import (
	"context"
	"fmt"
	"time"

	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	hostedManagementClusterEnvVar   = "HOSTED_MANAGEMENT_CLUSTER"
	hypershiftHostedClusterWorkflow = "hypershift-hostedcluster-workflow"
)

func ecTotalGaugeVec() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ephemeralclusters",
		Help: "The number of ephemeralclusters the controller created",
	}, []string{"konflux_cluster", "konflux_tenant", "cluster_profile", "workflow"})
}

type metricsGatherer struct {
	logger       *logrus.Entry
	client       ctrlruntimeclient.Client
	ecTotalGauge *prometheus.GaugeVec
	ecNS         string
	interval     time.Duration
}

func addMetricsToManager(logger *logrus.Entry, mgr manager.Manager, ecNS string, interval time.Duration) error {
	ecTotalGauge := ecTotalGaugeVec()

	if err := metrics.Registry.Register(ecTotalGauge); err != nil {
		return fmt.Errorf("failed to register ephemeralclusters metric: %w", err)
	}

	metricsGatherer := newMetricsGatherer(logger.WithField("controller", "ephemeral_cluster_metrics"),
		mgr.GetClient(), ecTotalGauge, ecNS, interval)
	if err := mgr.Add(metricsGatherer); err != nil {
		return fmt.Errorf("add metrics to manager: %w", err)
	}

	return nil
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

			mg.collect(&ecList)
		}
	}
}

func (mg *metricsGatherer) collect(ecList *ephemeralclusterv1.EphemeralClusterList) {
	ecTotal := make(map[struct {
		konfluxCluster string
		konfluxTenant  string
		clusterProfile string
		workflow       string
	}]uint)

	for i := range ecList.Items {
		ec := &ecList.Items[i]

		k := struct {
			konfluxCluster string
			konfluxTenant  string
			clusterProfile string
			workflow       string
		}{
			konfluxCluster: ec.KonfluxCluster(),
			konfluxTenant:  ec.KonfluxTenant(),
			clusterProfile: ec.Spec.CIOperator.Test.ClusterProfile,
			workflow:       ec.Spec.CIOperator.Test.Workflow,
		}

		// This combination of workflow and env var is used mainly by Konflux users.
		if hostedMgmt, ok := ec.Spec.CIOperator.Test.Env[hostedManagementClusterEnvVar]; ok && k.workflow == hypershiftHostedClusterWorkflow {
			k.workflow = k.workflow + "_" + hostedMgmt
		}

		ecTotal[k]++
	}

	mg.ecTotalGauge.Reset()
	for k, v := range ecTotal {
		mg.ecTotalGauge.WithLabelValues(k.konfluxCluster, k.konfluxTenant, k.clusterProfile, k.workflow).Set(float64(v))
	}
}

func (mg *metricsGatherer) NeedLeaderElection() bool { return true }

func newMetricsGatherer(logger *logrus.Entry, client ctrlruntimeclient.Client, ecTotalGauge *prometheus.GaugeVec,
	ecNS string, interval time.Duration) *metricsGatherer {
	return &metricsGatherer{
		logger:       logger,
		client:       client,
		ecTotalGauge: ecTotalGauge,
		ecNS:         ecNS,
		interval:     interval,
	}
}
