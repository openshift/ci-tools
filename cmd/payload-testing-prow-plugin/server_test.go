package main

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func (j1 jobSetSpecification) Equals(j2 jobSetSpecification) bool {
	return cmp.Equal(j1, j2, cmp.AllowUnexported(jobSetSpecification{}))
}

func TestSpecsFromComment(t *testing.T) {
	testCases := []struct {
		name     string
		comment  string
		expected []jobSetSpecification
	}{
		{
			name:     "/payload 4.10 nightly informing",
			comment:  "/payload 4.10 nightly informing",
			expected: []jobSetSpecification{{ocp: "4.10", releaseType: "nightly", jobs: "informing"}},
		},
		{
			name:     "/payload 4.8 ci all",
			comment:  "/payload 4.8 ci all",
			expected: []jobSetSpecification{{ocp: "4.8", releaseType: "ci", jobs: "all"}},
		},
		{
			name:    "/cmd 4.8 ci all",
			comment: "/cmd 4.8 ci all",
		},
		{
			name:     "multiple match",
			comment:  "/payload 4.10 nightly informing\n/payload 4.8 ci all",
			expected: []jobSetSpecification{{ocp: "4.10", releaseType: "nightly", jobs: "informing"}, {ocp: "4.8", releaseType: "ci", jobs: "all"}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := specsFromComment(tc.comment)
			if diff := cmp.Diff(tc.expected, actual, cmp.Comparer(func(x, y jobSetSpecification) bool {
				return cmp.Diff(x.ocp, y.ocp) == "" && cmp.Diff(x.releaseType, y.releaseType) == "" && cmp.Diff(x.jobs, y.jobs) == ""
			})); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

func TestMessage(t *testing.T) {
	testCases := []struct {
		name     string
		spec     jobSetSpecification
		expected string
	}{
		{
			name: "basic case",
			spec: jobSetSpecification{ocp: "4.10", releaseType: "nightly", jobs: "informing"},
			expected: `trigger 2 jobs of type informing for the nightly release of OCP 4.10
- dummy-ocp-4.10-nightly-informing-job1
- dummy-ocp-4.10-nightly-informing-job2
`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := message(tc.spec, fakeResolve(tc.spec.ocp, tc.spec.releaseType, tc.spec.jobs))
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

func fakeResolve(ocp string, releaseType releaseType, jobType jobType) []string {
	return []string{fmt.Sprintf("dummy-ocp-%s-%s-%s-job1", ocp, releaseType, jobType), fmt.Sprintf("dummy-ocp-%s-%s-%s-job2", ocp, releaseType, jobType)}
}
