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
	if len(pv.jobVolumes) != 0 && time.Since(pv.timestamp) < 24*time.Hour {
		return pv.jobVolumes, nil
	}
	v1api := prometheusapi.NewAPI(pv.promClient)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ts := time.Now().UTC().Add(-time.Duration(24*pv.prometheusDaysBefore) * time.Hour)
	jv, err := GetWeightedJobDemandFromPrometheus(ctx, v1api, ts, pv.options)
	if err != nil {
		return nil, err
	}
	pv.jobVolumes = jv
	pv.timestamp = time.Now()
	return pv.jobVolumes, nil
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
