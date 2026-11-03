package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestLoadLeaseCredentials(t *testing.T) {
	dir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	leaseServerCredentialsFile := filepath.Join(dir, "leaseServerCredentialsFile")
	if err := os.WriteFile(leaseServerCredentialsFile, []byte("ci-new:secret-new"), 0644); err != nil {
		t.Fatal(err)
	}

	leaseServerCredentialsInvalidFile := filepath.Join(dir, "leaseServerCredentialsInvalidFile")
	if err := os.WriteFile(leaseServerCredentialsInvalidFile, []byte("no-colon"), 0644); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name                       string
		leaseServerCredentialsFile string
		expectedUsername           string
		passwordGetterVerify       func(func() []byte) error
		expectedErr                error
	}{
		{
			name:                       "valid credential file",
			leaseServerCredentialsFile: leaseServerCredentialsFile,
			expectedUsername:           "ci-new",
			passwordGetterVerify: func(passwordGetter func() []byte) error {
				p := string(passwordGetter())
				if diff := cmp.Diff("secret-new", p); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:                       "wrong credential file",
			leaseServerCredentialsFile: leaseServerCredentialsInvalidFile,
			expectedErr:                fmt.Errorf("got invalid content of lease server credentials file which must be of the form '<username>:<passwrod>'"),
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			username, passwordGetter, err := loadLeaseCredentials(tc.leaseServerCredentialsFile)
			if diff := cmp.Diff(tc.expectedUsername, username); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
			if tc.passwordGetterVerify != nil {
				if err := tc.passwordGetterVerify(passwordGetter); err != nil {
					t.Errorf("unexpcected error: %v", err)
				}
			}
		})
	}
}

func TestAdjustLeaseAcquireTimeout(t *testing.T) {
	dur := func(d time.Duration) *prowapi.Duration {
		return &prowapi.Duration{Duration: d}
	}
	testCases := []struct {
		name            string
		initialTimeout  time.Duration
		tests           []api.TestStepConfiguration
		targets         []string
		expectedTimeout time.Duration
	}{
		{
			name:           "no matching test leaves timeout unchanged",
			initialTimeout: 2 * time.Hour,
			tests: []api.TestStepConfiguration{
				{As: "other", Timeout: dur(10 * time.Hour)},
			},
			targets:         []string{"e2e-parallel"},
			expectedTimeout: 2 * time.Hour,
		},
		{
			name:           "matching test with larger timeout adjusts lease timeout",
			initialTimeout: 2 * time.Hour,
			tests: []api.TestStepConfiguration{
				{As: "e2e-parallel", Timeout: dur(10 * time.Hour)},
			},
			targets:         []string{"e2e-parallel"},
			expectedTimeout: 10 * time.Hour,
		},
		{
			name:           "matching test with smaller timeout does not reduce lease timeout",
			initialTimeout: 2 * time.Hour,
			tests: []api.TestStepConfiguration{
				{As: "e2e-parallel", Timeout: dur(1 * time.Hour)},
			},
			targets:         []string{"e2e-parallel"},
			expectedTimeout: 2 * time.Hour,
		},
		{
			name:           "matching test with no timeout leaves lease timeout unchanged",
			initialTimeout: 2 * time.Hour,
			tests: []api.TestStepConfiguration{
				{As: "e2e-parallel"},
			},
			targets:         []string{"e2e-parallel"},
			expectedTimeout: 2 * time.Hour,
		},
		{
			name:           "no explicit targets with long test timeout adjusts lease timeout",
			initialTimeout: 2 * time.Hour,
			tests: []api.TestStepConfiguration{
				{As: "e2e-parallel", Timeout: dur(10 * time.Hour)},
			},
			targets:         nil,
			expectedTimeout: 10 * time.Hour,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			o := &options{
				leaseAcquireTimeout: tc.initialTimeout,
				configSpec: &api.ReleaseBuildConfiguration{
					Tests: tc.tests,
				},
			}
			o.targets.values = tc.targets
			o.adjustLeaseAcquireTimeout()
			if o.leaseAcquireTimeout != tc.expectedTimeout {
				t.Errorf("expected lease acquire timeout %s, got %s", tc.expectedTimeout, o.leaseAcquireTimeout)
			}
		})
	}
}
