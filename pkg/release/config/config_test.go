package config

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGetOrgRepoAndNumber(t *testing.T) {
	testCases := []struct {
		name           string
		input          AdditionalPR
		expectedOrg    string
		expectedRepo   string
		expectedNumber int
		expectedError  error
	}{
		{
			name:           "valid string",
			input:          "openshift/kubernetes#1234",
			expectedOrg:    "openshift",
			expectedRepo:   "kubernetes",
			expectedNumber: 1234,
		},
		{
			name:          "improperly formatted string",
			input:         "opens/hift/kubernetes#1234",
			expectedError: errors.New("string: opens/hift/kubernetes#1234 doesn't match expected format: org/repo#number"),
		},
		{
			name:          "number is not a number",
			input:         "openshift/kubernetes#somestring",
			expectedError: errors.New("string: openshift/kubernetes#somestring doesn't match expected format: org/repo#number"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			org, repo, number, err := tc.input.GetOrgRepoAndNumber()
			if diff := cmp.Diff(org, tc.expectedOrg); diff != "" {
				t.Fatalf("expectedOrg differs from actual: %s", diff)
			}
			if diff := cmp.Diff(repo, tc.expectedRepo); diff != "" {
				t.Fatalf("expectedRepo differs from actual: %s", diff)
			}
			if diff := cmp.Diff(number, tc.expectedNumber); diff != "" {
				t.Fatalf("expectedNumber differs from actual: %s", diff)
			}
			if diff := cmp.Diff(err, tc.expectedError, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("expectedErr differs from actual: %s", diff)
			}
		})
	}
}
