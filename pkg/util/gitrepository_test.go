package util

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

type cmdRecorder struct {
	output []byte
	err    error

	commands [][]string
}

func (r *cmdRecorder) run(argA, argB string) ([]byte, error) {
	r.commands = append(r.commands, []string{argA, argB})
	return r.output, r.err
}

func newCMDRecorder(output []byte, err error) cmdRecorder {
	return cmdRecorder{output: output, err: err}
}

func TestGetRemoteBranchCommitShaWithExeFunc(t *testing.T) {
	var testCases = []struct {
		name             string
		org              string
		repo             string
		branch           string
		execFunc         func(string, string) ([]byte, error)
		expected         string
		expectedError    error
		expectedCommands [][]string
	}{
		{
			name:             "basic case",
			org:              "org",
			repo:             "repo",
			branch:           "branch",
			expected:         "2f60da39a9d2e5cc00293b8ec7ad559fcd32446a",
			expectedCommands: [][]string{{"https://github.com/org/repo.git", "branch"}},
		},
		{
			name:             "wrong branch",
			org:              "org",
			repo:             "repo",
			branch:           "another-branch",
			expectedError:    fmt.Errorf("ref 'another-branch' does not point to any commit in 'org/repo' (did you mean 'branch'?)"),
			expectedCommands: [][]string{{"https://github.com/org/repo.git", "another-branch"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := newCMDRecorder([]byte(`2f60da39a9d2e5cc00293b8ec7ad559fcd32446a	refs/heads/branch
`), nil)
			tc.execFunc = r.run
			actual, actualError := getRemoteBranchCommitShaWithExecFunc(tc.org, tc.repo, tc.branch, tc.execFunc)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedCommands, r.commands); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
