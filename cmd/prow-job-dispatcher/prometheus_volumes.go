package main

import (
	"context"
	"sync"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config/secret"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

type prometheusVolumes struct {
	jobVolumes           map[string]float64
	timestamp            time.Time
	promClient           promapi.Client
	prometheusDaysBefore int
	m                    sync.Mutex
}

func newPrometheusVolumes(promOptions dispatcher.PrometheusOptions, prometheusDaysBefore int) (prometheusVolumes, error) {
	promClient, err := promOptions.NewPrometheusClient(secret.GetSecret)
	if err != nil {
		return prometheusVolumes{}, err
	}
	return prometheusVolumes{
		promClient:           promClient,
		jobVolumes:           map[string]float64{},
		prometheusDaysBefore: prometheusDaysBefore,
		m:                    sync.Mutex{},
	}, nil
}

func (pv *prometheusVolumes) GetJobVolumes() (map[string]float64, error) {
	pv.m.Lock()
	defer pv.m.Unlock()
	if len(pv.jobVolumes) != 0 && time.Since(pv.timestamp) < 24*time.Hour {
		logrus.Info("Using cached job volumes")
		return pv.jobVolumes, nil
	}
	v1api := prometheusapi.NewAPI(pv.promClient)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	y, m, d := time.Now().Add(-time.Duration(24*pv.prometheusDaysBefore) * time.Hour).Date()
	ts := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	jv, err := dispatcher.GetJobVolumesFromPrometheus(ctx, v1api, ts)
	if err != nil {
		return nil, err
	}
	pv.jobVolumes = jv
	pv.timestamp = time.Now()
	logrus.Info("Fetched new job volumes")
	return pv.jobVolumes, nil
}

func (pv *prometheusVolumes) getTotalVolume() float64 {
	var totalVolume float64
	for _, volume := range pv.jobVolumes {
		totalVolume += volume
	}

	return totalVolume
}

func (pv *prometheusVolumes) calculateVolumeDistribution(clusterMap dispatcher.ClusterMap) map[string]float64 {
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
