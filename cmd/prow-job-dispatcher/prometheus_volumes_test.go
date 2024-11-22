package main

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	promapi "github.com/prometheus/client_golang/api"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

type FakeClient struct {
}

func (fc *FakeClient) URL(ep string, args map[string]string) *url.URL {
	return &url.URL{}
}

func (fc *FakeClient) Do(ctx context.Context, req *http.Request) (*http.Response, []byte, error) {
	return &http.Response{}, []byte(""), nil
}

func TestPrometheusVolumesGetJobVolumes(t *testing.T) {
	type fields struct {
		jobVolumes map[string]float64
		timestamp  time.Time
		promClient promapi.Client
	}
	tests := []struct {
		name    string
		fields  fields
		want    map[string]float64
		wantErr bool
	}{
		{
			name: "acquire volumes from cache",
			fields: fields{
				jobVolumes: map[string]float64{"job1": 1.0, "job2": 2.0},
				timestamp:  time.Now(),
				promClient: nil,
			},
			want:    map[string]float64{"job1": 1.0, "job2": 2.0},
			wantErr: false,
		},
		{
			name: "acquire volumes from client - err as querying not tested",
			fields: fields{
				jobVolumes: map[string]float64{"job1": 1.0, "job2": 2.0},
				timestamp:  time.Now().Add(-25 * time.Hour),
				promClient: &FakeClient{},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pv := &prometheusVolumes{
				jobVolumes:           tt.fields.jobVolumes,
				timestamp:            tt.fields.timestamp,
				promClient:           tt.fields.promClient,
				prometheusDaysBefore: 15,
				m:                    sync.Mutex{},
			}
			got, err := pv.GetJobVolumes()
			if (err != nil) != tt.wantErr {
				t.Errorf("prometheusVolumes.GetJobVolumes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("prometheusVolumes.GetJobVolumes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalculateVolumeDistribution(t *testing.T) {
	tests := []struct {
		name       string
		jobVolumes map[string]float64
		clusterMap dispatcher.ClusterMap
		expected   map[string]float64
	}{
		{
			name:       "equal distribution",
			jobVolumes: map[string]float64{"jobA": 1000},
			clusterMap: dispatcher.ClusterMap{
				"clusterA": {Provider: "AWS", Capacity: 100},
				"clusterB": {Provider: "GCP", Capacity: 100},
			},
			expected: map[string]float64{
				"clusterA": 500,
				"clusterB": 500,
			},
		},
		{
			name:       "unequal distribution",
			jobVolumes: map[string]float64{"jobA": 1000},
			clusterMap: dispatcher.ClusterMap{
				"clusterA": {Provider: "AWS", Capacity: 70},
				"clusterB": {Provider: "GCP", Capacity: 30},
			},
			expected: map[string]float64{
				"clusterA": 700,
				"clusterB": 300,
			},
		},
		{
			name:       "multiple jobs with total distribution",
			jobVolumes: map[string]float64{"jobA": 500, "jobB": 500},
			clusterMap: dispatcher.ClusterMap{
				"clusterA": {Provider: "AWS", Capacity: 60},
				"clusterB": {Provider: "GCP", Capacity: 40},
			},
			expected: map[string]float64{
				"clusterA": 600,
				"clusterB": 400,
			},
		},
		{
			name:       "single cluster takes all",
			jobVolumes: map[string]float64{"jobA": 1000},
			clusterMap: dispatcher.ClusterMap{
				"clusterA": {Provider: "AWS", Capacity: 100},
			},
			expected: map[string]float64{
				"clusterA": 1000,
			},
		},
		{
			name:       "zero capacity clusters",
			jobVolumes: map[string]float64{"jobA": 1000},
			clusterMap: dispatcher.ClusterMap{
				"clusterA": {Provider: "AWS", Capacity: 0},
				"clusterB": {Provider: "GCP", Capacity: 100},
			},
			expected: map[string]float64{
				"clusterA": 0,
				"clusterB": 1000,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pv := &prometheusVolumes{
				jobVolumes:           tt.jobVolumes,
				timestamp:            time.Now(),
				promClient:           nil,
				prometheusDaysBefore: 15,
				m:                    sync.Mutex{},
			}
			if got := pv.calculateVolumeDistribution(tt.clusterMap); !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("prometheusVolumes.calculateVolumeDistribution() = %v, want %v", got, tt.expected)
			}
		})
	}
}
