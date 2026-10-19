package dispatcher

import (
	"testing"
	"time"
)

func TestDemandCacheFresh(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	testCases := []struct {
		name        string
		refreshedAt time.Time
		ttl         time.Duration
		want        bool
	}{
		{name: "never queried", ttl: time.Hour},
		{name: "fresh", refreshedAt: now.Add(-59 * time.Minute), ttl: time.Hour, want: true},
		{name: "expires at limit", refreshedAt: now.Add(-time.Hour), ttl: time.Hour},
		{name: "custom shorter cache", refreshedAt: now.Add(-10 * time.Minute), ttl: 5 * time.Minute},
		{name: "clock rollback", refreshedAt: now.Add(time.Second), ttl: time.Hour},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := demandCacheFresh(tc.refreshedAt, now, tc.ttl); got != tc.want {
				t.Errorf("cache freshness = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestDemandCacheTTLValidation(t *testing.T) {
	testCases := []struct {
		name    string
		ttl     time.Duration
		wantErr bool
	}{
		{name: "default"},
		{name: "short cache", ttl: time.Minute},
		{name: "maximum", ttl: 24 * time.Hour},
		{name: "negative", ttl: -time.Second, wantErr: true},
		{name: "too old", ttl: 25 * time.Hour, wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			options := PrometheusOptions{DemandCacheTTL: tc.ttl}
			if err := options.Validate(); (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, want error %t", err, tc.wantErr)
			}
		})
	}
}
