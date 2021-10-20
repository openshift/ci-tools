package main

import (
	"path/filepath"
	"testing"
)

func TestAllDataReadyToConsume(t *testing.T) {
	cache := &localCache{dir: filepath.Join("testdata", "localCache")}

	testCases := []struct {
		name     string
		loaders  map[string][]*cacheReloader
		expected bool
	}{
		{
			name: "ready",
			loaders: map[string][]*cacheReloader{
				MetricNameCPUUsage: {
					{
						name:  "pods/" + MetricNameCPUUsage,
						cache: cache,
					},
				},
			},
			expected: true,
		},
		{
			name: "not ready",
			loaders: map[string][]*cacheReloader{
				MetricNameMemoryWorkingSet: {
					{
						name:  "pods/" + MetricNameMemoryWorkingSet,
						cache: cache,
					},
				},
			},
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if result := allDataReadyToConsume(tc.loaders); result != tc.expected {
				t.Fatalf("result of %v does not match expected of %v", result, tc.expected)
			}
		})
	}
}
