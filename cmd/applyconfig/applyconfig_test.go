package main

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
)

func TestIsAdminConfig(t *testing.T) {
	testCases := []struct {
		filename string
		expected bool
	}{
		{
			filename: "admin_01_something_rbac.yaml",
			expected: true,
		},
		{
			filename: "admin_something_rbac.yaml",
			expected: true,
		},
		// Negative
		{filename: "cfg_01_something"},
		{filename: "admin_01_something_rbac"},
		{filename: "admin_01_something_rbac.yml"},
		{filename: "admin.yaml"},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			is := isAdminConfig(tc.filename)
			if is != tc.expected {
				t.Errorf("expected %t, got %t", tc.expected, is)
			}
		})
	}
}

func TestIsStandardConfig(t *testing.T) {
	testCases := []struct {
		filename string
		expected bool
	}{
		{
			filename: "01_something_rbac.yaml",
			expected: true,
		},
		{
			filename: "something_rbac.yaml",
			expected: true,
		},
		// Negative
		{filename: "admin_01_something.yaml"},
		{filename: "cfg_01_something_rbac"},
		{filename: "cfg_01_something_rbac.yml"},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			is := isStandardConfig(tc.filename)
			if is != tc.expected {
				t.Errorf("expected %t, got %t", tc.expected, is)
			}
		})
	}
}

func TestOcApply(t *testing.T) {
	testCases := []struct {
		name string

		path string
		user string
		dry  bool

		expected string
	}{
		{
			name:     "no user, not dry",
			path:     "/path/to/file",
			expected: "oc apply -f /path/to/file",
		},
		{
			name:     "no user, dry",
			path:     "/path/to/different/file",
			dry:      true,
			expected: "oc apply -f /path/to/different/file --dry-run",
		},
		{
			name:     "user, dry",
			path:     "/path/to/file",
			dry:      true,
			user:     "joe",
			expected: "oc apply -f /path/to/file --dry-run --as joe",
		},
		{
			name:     "user, not dry",
			path:     "/path/to/file",
			user:     "joe",
			expected: "oc apply -f /path/to/file --as joe",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := makeOcApply(tc.path, tc.user, tc.dry)
			command := strings.Join(cmd.Args, " ")
			if !reflect.DeepEqual(command, tc.expected) {
				t.Errorf("Expected '%v', got '%v'", tc.expected, command)
			}
		})
	}
}

type mockExecutor struct {
	t *testing.T

	calls     []*exec.Cmd
	responses []error
}

func (m *mockExecutor) runAndCheck(cmd *exec.Cmd, _ string) ([]byte, error) {
	responseIdx := len(m.calls)
	m.calls = append(m.calls, cmd)

	if len(m.responses) < responseIdx+1 {
		m.t.Fatalf("mockExecutor received unexpected call: %v", cmd.Args)
	}
	return []byte("MOCK OUTPUT"), m.responses[responseIdx]
}

func (m *mockExecutor) getCalls() []string {
	var calls []string
	for _, call := range m.calls {
		calls = append(calls, strings.Join(call.Args, " "))
	}

	return calls
}

func TestAsGenericManifest(t *testing.T) {
	testCases := []struct {
		description string
		applier     *configApplier
		executions  []error

		expectedCalls []string
		expectedError bool
	}{
		{
			description:   "success: oc apply -f path",
			applier:       &configApplier{path: "path"},
			executions:    []error{nil}, // expect a single successful call
			expectedCalls: []string{"oc apply -f path"},
		},
		{
			description:   "success: oc apply -f path --dry-run",
			applier:       &configApplier{path: "path", dry: true},
			executions:    []error{nil}, // expect a single successful call
			expectedCalls: []string{"oc apply -f path --dry-run"},
		},
		{
			description:   "success: oc apply -f path --dry-run --as user",
			applier:       &configApplier{path: "path", user: "user", dry: true},
			executions:    []error{nil}, // expect a single successful call
			expectedCalls: []string{"oc apply -f path --dry-run --as user"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			executor := &mockExecutor{t: t, responses: tc.executions}
			tc.applier.executor = executor
			err := tc.applier.asGenericManifest()
			if err != nil && !tc.expectedError {
				t.Errorf("returned unexpected error: %v", err)
			}
			if err == nil && tc.expectedError {
				t.Error("expected error, was not returned")
			}

			calls := executor.getCalls()
			if !reflect.DeepEqual(tc.expectedCalls, calls) {
				t.Errorf("calls differ from expected:\n%s", diff.ObjectReflectDiff(tc.expectedCalls, calls))
			}
		})
	}
}
