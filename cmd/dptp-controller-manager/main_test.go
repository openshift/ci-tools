package main

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/flagutil"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestCompleteImageStreamTags(t *testing.T) {
	tests := []struct {
		name           string
		flagName       string
		raw            flagutil.Strings
		expected       sets.Set[string]
		expectedErrors []error
	}{
		{
			name:     "no flags",
			flagName: "some-flag",
			expected: sets.New[string](),
		},
		{
			name:           "some flag: wrong format",
			flagName:       "some-flag",
			raw:            flagutil.NewStrings([]string{"namespace/name:tag", "xyz"}...),
			expected:       sets.New[string]("namespace/name:tag"),
			expectedErrors: []error{fmt.Errorf("--some-flag value xyz was not in namespace/name:tag format")},
		},
		{
			name:     "some flags",
			flagName: "some-flag",
			raw:      flagutil.NewStrings([]string{"ci/applyconfig:latest", "ocp/4.6:cli"}...),
			expected: sets.New[string]([]string{"ci/applyconfig:latest", "ocp/4.6:cli"}...),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErrors := completeImageStreamTags(tc.flagName, tc.raw)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedErrors, actualErrors, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
		})
	}
}

func TestCompleteImageStream(t *testing.T) {
	tests := []struct {
		name           string
		flagName       string
		raw            flagutil.Strings
		expected       sets.Set[string]
		expectedErrors []error
	}{
		{
			name:     "no flags",
			flagName: "some-flag",
			expected: sets.New[string](),
		},
		{
			name:           "some flag: wrong format",
			flagName:       "some-flag",
			raw:            flagutil.NewStrings([]string{"namespace/name", "xyz"}...),
			expected:       sets.New[string]("namespace/name"),
			expectedErrors: []error{fmt.Errorf("--some-flag value xyz was not in namespace/name format")},
		},
		{
			name:     "some flags",
			flagName: "some-flag",
			raw:      flagutil.NewStrings([]string{"ci/applyconfig", "ocp/4.6"}...),
			expected: sets.New[string]([]string{"ci/applyconfig", "ocp/4.6"}...),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErrors := completeImageStream(tc.flagName, tc.raw)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedErrors, actualErrors, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
		})
	}
}

func TestCompleteSet(t *testing.T) {
	tests := []struct {
		name     string
		raw      flagutil.Strings
		expected sets.Set[string]
	}{
		{
			name:     "no flags",
			expected: sets.New[string](),
		},
		{
			name:     "some flags",
			raw:      flagutil.NewStrings([]string{"abc", "xyz"}...),
			expected: sets.New[string]([]string{"abc", "xyz"}...),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.expected, completeSet(tc.raw)); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
		})
	}
}
