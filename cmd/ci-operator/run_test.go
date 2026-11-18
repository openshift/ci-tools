package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestExcludeContextCancelledErrors(t *testing.T) {
	testCases := []struct {
		id       string
		errs     []error
		expected []error
	}{
		{
			id: "no context cancelled errors, no changes expected",
			errs: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step bar failed: oopsie"),
			},
			expected: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step bar failed: oopsie"),
			},
		},
		{
			id: "context cancelled errors, changes expected",
			errs: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
				results.ForReason("step_failed").WithError(context.Canceled).Errorf("step bar failed: %v", context.Canceled),
			},
			expected: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
			},
		},
	}

	for _, tc := range testCases {
		actualErrs := excludeContextCancelledErrors(tc.errs)
		if diff := cmp.Diff(actualErrs, tc.expected, testhelper.EquateErrorMessage); diff != "" {
			t.Fatal(diff)
		}
	}
}
