package main

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	flagutil "k8s.io/test-infra/prow/flagutil/config"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

var (
	sevenDays = 7 * 24 * time.Hour
)

func TestGatherOptions(t *testing.T) {
	defer func() {
		logrus.StandardLogger().ExitFunc = nil
	}()
	var fatalErr bool
	logrus.StandardLogger().ExitFunc = func(int) { fatalErr = true }

	testCases := []struct {
		name     string
		args     []string
		expected options
	}{
		{
			name: "default",
			args: []string{"cmd"},
			expected: options{
				runOnce:        false,
				dryRun:         true,
				interval:       time.Hour,
				cacheFile:      "",
				cacheRecordAge: sevenDays,
				configFile:     "",
			},
		},
		{
			name: "basic case",
			args: []string{"cmd", "--run-once=true", "--interval=2h", "--cache-file=cache.yaml", "--cache-record-age=100h", "--config-file=config.yaml"},
			expected: options{
				runOnce:        true,
				dryRun:         true,
				interval:       2 * time.Hour,
				cacheFile:      "cache.yaml",
				cacheRecordAge: 100 * time.Hour,
				configFile:     "config.yaml",
			},
		},
		{
			name:     "wrong interval and empty cache record age",
			args:     []string{"cmd", "--interval=notNumber", "--cache-record-age="},
			expected: options{dryRun: true},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args
			actual := gatherOptions()
			if !fatalErr {
				if diff := cmp.Diff(tc.expected.runOnce, actual.runOnce); diff != "" {
					t.Errorf("%s runOnce differs from expected:\n%s", tc.name, diff)
				}
				if diff := cmp.Diff(tc.expected.dryRun, actual.dryRun); diff != "" {
					t.Errorf("%s dryRun differs from expected:\n%s", tc.name, diff)
				}
				if diff := cmp.Diff(tc.expected.interval, actual.interval); diff != "" {
					t.Errorf("%s interval differs from expected:\n%s", tc.name, diff)
				}
				if diff := cmp.Diff(tc.expected.cacheFile, actual.cacheFile); diff != "" {
					t.Errorf("%s cacheFile differs from expected:\n%s", tc.name, diff)
				}
				if diff := cmp.Diff(tc.expected.cacheRecordAge, actual.cacheRecordAge); diff != "" {
					t.Errorf("%s cacheRecordAge differs from expected:\n%s", tc.name, diff)
				}
				if diff := cmp.Diff(tc.expected.configFile, actual.configFile); diff != "" {
					t.Errorf("%s configFile differs from expected:\n%s", tc.name, diff)
				}
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
				runOnce:        false,
				dryRun:         true,
				interval:       time.Hour,
				cacheFile:      "",
				cacheRecordAge: sevenDays,
				configFile:     "/etc/retester/config.yaml",
			},
		},
		{
			name: "no-config-file",
			o: options{
				config:         flagutil.ConfigOptions{ConfigPath: "/etc/config/config.yaml"},
				runOnce:        false,
				dryRun:         true,
				interval:       time.Hour,
				cacheFile:      "",
				cacheRecordAge: sevenDays,
				configFile:     "",
			},
			expected: errors.New("--config-file is mandatory, configuration file path of the retest is empty"),
		},
		{
			name: "no-config-path",
			o: options{
				//not set config path results: error(*errors.errorString) *{s: "-- is mandatory"}
				config: flagutil.ConfigOptions{ConfigPathFlagName: "config-path"},
			},
			expected: errors.New("--config-path is mandatory"),
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
