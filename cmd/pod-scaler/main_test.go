package main

import (
	"testing"
)

func TestConsumerOptions_authoritativeConfig(t *testing.T) {
	testCases := []struct {
		name string
		opts consumerOptions
		want authoritativeConfig
	}{
		{
			name: "apply enabled",
			opts: consumerOptions{
				authoritativeCPURequest:    true,
				authoritativeCPULimit:      true,
				authoritativeMemoryRequest: true,
				authoritativeMemoryLimit:   true,
			},
			want: authoritativeConfig{
				cpuRequest:    authoritativePair{apply: true},
				cpuLimit:      authoritativePair{apply: true},
				memoryRequest: authoritativePair{apply: true},
				memoryLimit:   authoritativePair{apply: true},
			},
		},
		{
			name: "apply disabled implies dry-run",
			opts: consumerOptions{
				authoritativeCPURequest:    false,
				authoritativeCPULimit:      false,
				authoritativeMemoryRequest: false,
				authoritativeMemoryLimit:   false,
			},
			want: authoritativeConfig{
				cpuRequest:    authoritativePair{apply: false},
				cpuLimit:      authoritativePair{apply: false},
				memoryRequest: authoritativePair{apply: false},
				memoryLimit:   authoritativePair{apply: false},
			},
		},
		{
			name: "legacy authoritative cpu enables apply",
			opts: consumerOptions{
				authoritativeCPU: true,
			},
			want: authoritativeConfig{
				cpuRequest:    authoritativePair{apply: true},
				cpuLimit:      authoritativePair{apply: true},
				memoryRequest: authoritativePair{apply: false},
				memoryLimit:   authoritativePair{apply: false},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opts.authoritativeConfig()
			assertAuthoritativeConfigEqual(t, tc.want, got)
		})
	}
}

func assertAuthoritativePairEqual(t *testing.T, want, got authoritativePair) {
	t.Helper()
	if want.apply != got.apply {
		t.Fatalf("authoritativePair mismatch: want apply=%v, got apply=%v", want.apply, got.apply)
	}
}

func assertAuthoritativeConfigEqual(t *testing.T, want, got authoritativeConfig) {
	t.Helper()
	assertAuthoritativePairEqual(t, want.cpuRequest, got.cpuRequest)
	assertAuthoritativePairEqual(t, want.cpuLimit, got.cpuLimit)
	assertAuthoritativePairEqual(t, want.memoryRequest, got.memoryRequest)
	assertAuthoritativePairEqual(t, want.memoryLimit, got.memoryLimit)
}
