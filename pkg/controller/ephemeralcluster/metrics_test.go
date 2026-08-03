package ephemeralcluster

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

type metric struct {
	Labels []string
	Value  float64
}

func ec(cluster, tenant, clusterProfile, workflow, hostedMgmtClusterEnv string) *ephemeralclusterv1.EphemeralCluster {
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
	}

	if hostedMgmtClusterEnv != "" {
		ec.Spec.CIOperator.Test.Env = map[string]string{
			hostedManagementClusterEnvVar: hostedMgmtClusterEnv,
		}
	}

	return &ec
}

func collectGauge(v *prometheus.MetricVec) (result []metric, err error) {
	metricCh := make(chan prometheus.Metric)
	wg := sync.WaitGroup{}

	wg.Go(func() {
		for m := range metricCh {
			dtoMetric := dto.Metric{}
			if writeErr := m.Write(&dtoMetric); writeErr != nil {
				err = fmt.Errorf("write gauge: %w", writeErr)
				return
			}

			m := metric{Value: *dtoMetric.Gauge.Value}
			for _, l := range dtoMetric.Label {
				m.Labels = append(m.Labels, *l.Value)
			}

			result = append(result, m)
		}
	})

	v.Collect(metricCh)
	close(metricCh)
	wg.Wait()

	return
}

func TestStart(t *testing.T) {
	t.Parallel()

	const gatherInterval = time.Second
	cmpMetricOpts := []cmp.Option{
		cmpopts.SortSlices(func(a, b metric) bool {
			aLabels := strings.Join(a.Labels, "")
			bLabels := strings.Join(b.Labels, "")
			return strings.Compare(aLabels, bLabels) < 0
		}),
		cmp.Comparer(func(a, b metric) bool {
			sort.Strings(a.Labels)
			sort.Strings(b.Labels)
			aLabels := strings.Join(a.Labels, "")
			bLabels := strings.Join(b.Labels, "")
			return strings.Compare(aLabels, bLabels) == 0 && a.Value == b.Value
		}),
	}

	scheme := runtime.NewScheme()
	sb := runtime.NewSchemeBuilder(ephemeralclusterv1.AddToScheme, prowv1.AddToScheme, corev1.AddToScheme)
	if err := sb.AddToScheme(scheme); err != nil {
		t.Fatalf("build scheme: %s", err.Error())
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(func() { cancel() })

		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		ecTotalGauge := ecTotalGaugeVec()

		mg := newMetricsGatherer(logrus.NewEntry(logrus.StandardLogger()), client, ecTotalGauge,
			EphemeralClusterNamespace, gatherInterval)
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
					ec("stone-prd-rh01", "tp-ci-tenant", "aws", "e2e-aws-workflow", ""),
					ec("stone-prd-rh01", "tp-ci-tenant", "aws", "e2e-aws-workflow", ""),
					ec("stone-stg-rh01", "tp-ci-tenant-2", "aws-2", "e2e-aws-workflow", ""),
				},
				wantMetricVals: []metric{{
					Labels: []string{"stone-prd-rh01", "tp-ci-tenant", "aws", "e2e-aws-workflow"},
					Value:  2,
				}, {
					Labels: []string{"stone-stg-rh01", "tp-ci-tenant-2", "aws-2", "e2e-aws-workflow"},
					Value:  1,
				}},
			},
			{
				ecs: []*ephemeralclusterv1.EphemeralCluster{
					ec("stone-stg-rh01", "tp-ci-tenant-2", "aws-2", "e2e-aws-workflow", ""),
					ec("stone-prd-rh01", "amisstea-tenant", "aws-konflux-prod", "hypershift-hostedcluster-workflow", "hosted-mgmt2"),
				},
				wantMetricVals: []metric{{
					Labels: []string{"stone-stg-rh01", "tp-ci-tenant-2", "aws-2", "e2e-aws-workflow"},
					Value:  1,
				}, {
					Labels: []string{"stone-prd-rh01", "amisstea-tenant", "aws-konflux-prod", "hypershift-hostedcluster-workflow_hosted-mgmt2"},
					Value:  1,
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

			gotMetrics, err := collectGauge(ecTotalGauge.MetricVec)
			if err != nil {
				t.Fatalf("collect gauge: %s", err.Error())
			}

			if diff := cmp.Diff(round.wantMetricVals, gotMetrics, cmpMetricOpts...); diff != "" {
				t.Errorf("Round %d - Unexpected metric: %s\n", i, diff)
			}
		}
	})
}
