package main

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	flagutil "k8s.io/test-infra/prow/flagutil/config"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

var (
	sevenDays = 7 * 24 * time.Hour
)

func TestGatherOptions(t *testing.T) {
	testCases := []struct {
		name     string
		args     []string
		expected options
	}{
		{
			name: "default",
			args: []string{"cmd"},
			expected: options{
				dryRun:            true,
				intervalRaw:       "1h",
				cacheRecordAgeRaw: "168h",
			},
		},
		{
			name: "basic case",
			args: []string{"cmd", "--run-once=true", "--interval=2h", "--cache-file=cache.yaml", "--cache-record-age=100h", "--config-file=config.yaml"},
			expected: options{
				runOnce:           true,
				dryRun:            true,
				intervalRaw:       "2h",
				cacheFile:         "cache.yaml",
				cacheRecordAgeRaw: "100h",
				configFile:        "config.yaml",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args
			actual := gatherOptions()
			if diff := cmp.Diff(tc.expected.runOnce, actual.runOnce); diff != "" {
				t.Errorf("%s run once differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expected.dryRun, actual.dryRun); diff != "" {
				t.Errorf("%s dry run differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expected.intervalRaw, actual.intervalRaw); diff != "" {
				t.Errorf("%s interval raw differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expected.cacheFile, actual.cacheFile); diff != "" {
				t.Errorf("%s cache file differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expected.cacheRecordAgeRaw, actual.cacheRecordAgeRaw); diff != "" {
				t.Errorf("%s cache record age raw differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expected.configFile, actual.configFile); diff != "" {
				t.Errorf("%s config file differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	testCases := []struct {
		name     string
		o        options
		expected error
	}{
		{
			name: "basic",
			o: options{
				config:         flagutil.ConfigOptions{ConfigPath: "/etc/config/config.yaml"},
				dryRun:         true,
				interval:       time.Hour,
				cacheRecordAge: sevenDays,
				configFile:     "/etc/retester/config.yaml",
			},
		},
		{
			name: "no-config-file",
			o: options{
				config:         flagutil.ConfigOptions{ConfigPath: "/etc/config/config.yaml"},
				dryRun:         true,
				interval:       time.Hour,
				cacheRecordAge: sevenDays,
			},
			expected: errors.New("--config-file is required"),
		},
		{
			name: "no-config-path",
			o: options{
				//not set config path results: error(*errors.errorString) *{s: "-- is mandatory"}
				config: flagutil.ConfigOptions{ConfigPathFlagName: "config-path"},
			},
			expected: errors.New("--config-path is mandatory"),
		},
		{
			name: "cache-file not set when using aws",
			o: options{
				config:         flagutil.ConfigOptions{ConfigPath: "/etc/config/config.yaml"},
				configFile:     "/etc/retester/config.yaml",
				dryRun:         true,
				interval:       time.Hour,
				cacheRecordAge: sevenDays,
				cacheFileOnS3:  true,
			},
			expected: errors.New("--cache-file is required if --cache-file-on-s3 is set to true"),
		},
		{
			name: "cache-file not set when using local file cache",
			o: options{
				config:         flagutil.ConfigOptions{ConfigPath: "/etc/config/config.yaml"},
				configFile:     "/etc/retester/config.yaml",
				dryRun:         true,
				interval:       time.Hour,
				cacheRecordAge: sevenDays,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.o.Validate()
			if diff := cmp.Diff(tc.expected, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
		})
	}
}

func TestComplete(t *testing.T) {
	testCases := []struct {
		name                   string
		o                      options
		expected               error
		expectedInterval       time.Duration
		expectedCacheRecordAge time.Duration
	}{
		{
			name: "basic",
			o: options{
				intervalRaw:       "1h",
				cacheRecordAgeRaw: "168h",
			},
			expectedInterval:       time.Hour,
			expectedCacheRecordAge: sevenDays,
		},
		{
			name: "wrong format",
			o: options{
				intervalRaw:       "wrong format",
				cacheRecordAgeRaw: "168h",
			},
			expected: errors.New("invalid --interval: time: invalid duration \"wrong format\""),
		}, {
			name: "empty",
			o: options{
				intervalRaw: "1h",
			},
			expected:         errors.New("invalid --cache-record-age: time: invalid duration \"\""),
			expectedInterval: time.Hour,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.o.complete()
			if diff := cmp.Diff(tc.expected, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedInterval, tc.o.interval); diff != "" {
				t.Errorf("%s interval differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedCacheRecordAge, tc.o.cacheRecordAge); diff != "" {
				t.Errorf("%s cache record age differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}
