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
