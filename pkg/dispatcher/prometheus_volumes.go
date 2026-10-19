package dispatcher

import (
	"context"
	"sync"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"

	"sigs.k8s.io/prow/pkg/config/secret"
)

type prometheusVolumes struct {
	jobVolumes           map[string]float64
	timestamp            time.Time
	evaluationTimestamp  time.Time
	promClient           promapi.Client
	prometheusDaysBefore int
	options              PrometheusOptions
	m                    sync.Mutex
}

func NewPrometheusVolumes(promOptions PrometheusOptions, prometheusDaysBefore int) (prometheusVolumes, error) {
	promOptions.demandDefaults()
	promClient, err := promOptions.NewPrometheusClient(secret.GetSecret)
	if err != nil {
		return prometheusVolumes{}, err
	}
	return prometheusVolumes{
		promClient:           promClient,
		jobVolumes:           map[string]float64{},
		prometheusDaysBefore: prometheusDaysBefore,
		options:              promOptions,
		m:                    sync.Mutex{},
	}, nil
}

func (pv *prometheusVolumes) GetJobVolumes() (map[string]float64, error) {
	pv.m.Lock()
	defer pv.m.Unlock()
	pv.options.demandDefaults()
	now := time.Now().UTC()
	if demandCacheFresh(pv.timestamp, now, pv.options.DemandCacheTTL) {
		return pv.jobVolumes, nil
	}
	v1api := prometheusapi.NewAPI(pv.promClient)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ts := now.Add(-time.Duration(24*pv.prometheusDaysBefore) * time.Hour)
	jv, err := GetWeightedJobDemandFromPrometheus(ctx, v1api, ts, pv.options)
	if err != nil {
		demandQueryErrorsMetric.Inc()
		return nil, err
	}
	pv.jobVolumes = jv
	pv.timestamp = time.Now()
	pv.evaluationTimestamp = ts
	demandRefreshTimestampMetric.Set(float64(pv.timestamp.Unix()))
	demandEvaluationTimestampMetric.Set(float64(ts.Unix()))
	demandSamplesMetric.Set(float64(len(jv)))
	return pv.jobVolumes, nil
}

// demandCacheFresh rejects clock rollback and expires empty successful responses too.
func demandCacheFresh(refreshedAt, now time.Time, ttl time.Duration) bool {
	return !refreshedAt.IsZero() && !now.Before(refreshedAt) && now.Sub(refreshedAt) < ttl
}

func (pv *prometheusVolumes) getTotalVolume() float64 {
	var totalVolume float64
	for _, volume := range pv.jobVolumes {
		totalVolume += volume
	}

	return totalVolume
}

func (pv *prometheusVolumes) CalculateVolumeDistribution(clusterMap ClusterMap) map[string]float64 {
	totalCapacity := 0
	for _, cluster := range clusterMap {
		totalCapacity += cluster.Capacity
	}
	totalVolume := pv.getTotalVolume()
	volumeDistribution := make(map[string]float64)
	for clusterName, cluster := range clusterMap {
		volumeShare := (float64(cluster.Capacity) / float64(totalCapacity)) * totalVolume
		volumeDistribution[clusterName] = volumeShare
	}

	return volumeDistribution
}
