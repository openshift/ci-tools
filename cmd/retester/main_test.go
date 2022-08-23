package main

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	flagutil "k8s.io/test-infra/prow/flagutil/config"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

var (
	hour      = time.Duration(1000000000 * 3600)
	sevenDays = 168 * hour
)

func TestGatherOptions(t *testing.T) {
	defer func() {
		logrus.StandardLogger().ExitFunc = nil
	}()
	var fatalErr bool
	logrus.StandardLogger().ExitFunc = func(int) { fatalErr = true }

	testCases := []struct {
		name        string
		args        []string
		expected    options
		expectFatal bool
	}{
		{
			name: "default",
			args: []string{"cmd"},
			expected: options{
				runOnce:        false,
				dryRun:         true,
				interval:       hour,
				cacheFile:      "",
				cacheRecordAge: sevenDays,
				configFile:     "",
			},
			expectFatal: false,
		},
		{
			name: "basic case",
			args: []string{"cmd", "--run-once=true", "--interval=2h", "--cache-file=cache.yaml", "--cache-record-age=100h", "--config-file=config.yaml"},
			expected: options{
				runOnce:        true,
				dryRun:         true,
				interval:       2 * hour,
				cacheFile:      "cache.yaml",
				cacheRecordAge: 100 * hour,
				configFile:     "config.yaml",
			},
			expectFatal: false,
		},
		{
			name:        "wrong interval and empty cache record age",
			args:        []string{"cmd", "--interval=notNumber", "--cache-record-age="},
			expected:    options{dryRun: true},
			expectFatal: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fatalErr = false
			os.Args = tc.args
			actual := gatherOptions()
			if diff := cmp.Diff(tc.expectFatal, fatalErr); diff != "" {
				t.Errorf("Unexpected fatal error:\n%s", diff)
			} else {
				opts := []cmp.Option{
					cmpopts.IgnoreFields(options{}, "config", "github"),
					cmp.AllowUnexported(options{}),
				}
				if diff := cmp.Diff(tc.expected, actual, opts...); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
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
				interval:       hour,
				cacheFile:      "",
				cacheRecordAge: sevenDays,
				configFile:     "",
			},
			expected: nil,
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
