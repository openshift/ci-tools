package ephemeralcluster

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
)

func ec(cluster, tenant, clusterProfile, workflow, hostedMgmtClusterEnv string, phase ephemeralclusterv1.EphemeralClusterPhase) *ephemeralclusterv1.EphemeralCluster {
	ec := ephemeralclusterv1.EphemeralCluster{
		ObjectMeta: v1.ObjectMeta{
			Annotations: map[string]string{
				ephemeralclusterv1.KonfluxClusterAnnotation: cluster,
				ephemeralclusterv1.KonfluxTenantAnnotation:  tenant,
			},
			Namespace: EphemeralClusterNamespace,
			Name:      uuid.NewString(),
		},
		Spec: ephemeralclusterv1.EphemeralClusterSpec{
			CIOperator: ephemeralclusterv1.CIOperatorSpec{
				Test: ephemeralclusterv1.TestSpec{
					ClusterProfile: clusterProfile,
					Workflow:       workflow,
				},
			},
		},
		Status: ephemeralclusterv1.EphemeralClusterStatus{
			Phase: phase,
		},
	}

	if hostedMgmtClusterEnv != "" {
		ec.Spec.CIOperator.Test.Env = map[string]string{
			hostedManagementClusterEnvVar: hostedMgmtClusterEnv,
		}
	}

	return &ec
}

func TestStart(t *testing.T) {
	t.Parallel()

	const gatherInterval = time.Second
	scheme := fakeScheme(t)

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(func() { cancel() })

		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		countGauge := newCountGaugeVec()
		provisioningDurationHistogram := newProvisioningDurationHistogramVec()

		deprovisioningDurationHistogram := newDeprovisioningDurationHistogramVec()

		mg := newMetricsGatherer(logrus.NewEntry(logrus.StandardLogger()), client,
			countGauge, provisioningDurationHistogram, deprovisioningDurationHistogram, EphemeralClusterNamespace, gatherInterval)
		go func() {
			if err := mg.Start(ctx); err != nil {
				t.Errorf("Failed to start metrics gatherer: %s", err.Error())
			}
		}()

		for i, round := range []struct {
			ecs            []*ephemeralclusterv1.EphemeralCluster
			wantMetricVals []metric
		}{
			{
				ecs: []*ephemeralclusterv1.EphemeralCluster{
					ec("stone-prd-rh01", "tp-ci-tenant", "aws", "e2e-aws-workflow", "", ephemeralclusterv1.EphemeralClusterProvisioning),
					ec("stone-prd-rh01", "tp-ci-tenant", "aws", "e2e-aws-workflow", "", ephemeralclusterv1.EphemeralClusterProvisioning),
					ec("stone-stg-rh01", "tp-ci-tenant-2", "aws-2", "e2e-aws-workflow", "", ephemeralclusterv1.EphemeralClusterFailed),
				},
				wantMetricVals: []metric{{
					Gauge: &gauge{
						Labels: []string{"stone-prd-rh01", "tp-ci-tenant", "aws", "e2e-aws-workflow", "Provisioning"},
						Value:  2,
					},
				}, {
					Gauge: &gauge{
						Labels: []string{"stone-stg-rh01", "tp-ci-tenant-2", "aws-2", "e2e-aws-workflow", "Failed"},
						Value:  1,
					},
				}},
			},
			{
				ecs: []*ephemeralclusterv1.EphemeralCluster{
					ec("stone-stg-rh01", "tp-ci-tenant-2", "aws-2", "e2e-aws-workflow", "", ephemeralclusterv1.EphemeralClusterFailed),
					ec("stone-prd-rh01", "amisstea-tenant", "aws-konflux-prod", "hypershift-hostedcluster-workflow", "hosted-mgmt2", ephemeralclusterv1.EphemeralClusterDeprovisioning),
				},
				wantMetricVals: []metric{{
					Gauge: &gauge{
						Labels: []string{"stone-stg-rh01", "tp-ci-tenant-2", "aws-2", "e2e-aws-workflow", "Failed"},
						Value:  1,
					},
				}, {
					Gauge: &gauge{
						Labels: []string{"stone-prd-rh01", "amisstea-tenant", "aws-konflux-prod", "hypershift-hostedcluster-workflow_hosted-mgmt2", "Deprovisioning"},
						Value:  1,
					},
				}},
			},
			{},
		} {
			if err := client.DeleteAllOf(ctx, &ephemeralclusterv1.EphemeralCluster{},
				ctrlruntimeclient.InNamespace(EphemeralClusterNamespace)); err != nil {
				t.Fatalf("delete all ephemeral clusters: %s", err.Error())
			}

			for _, ec := range round.ecs {
				if err := client.Create(ctx, ec); err != nil {
					t.Fatalf("create ephemeral cluster: %s", err.Error())
				}
			}

			time.Sleep(gatherInterval)
			synctest.Wait()

			gotMetrics, err := collectGauge(countGauge.MetricVec)
			if err != nil {
				t.Fatalf("collect gauge: %s", err.Error())
			}

			if diff := cmpMetrics(round.wantMetricVals, gotMetrics); diff != "" {
				t.Errorf("Round %d - Unexpected metric: %s\n", i, diff)
			}
		}
	})
}
