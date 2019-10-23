package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	templateapi "github.com/openshift/api/template/v1"
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

func TestMakeOcCommand(t *testing.T) {
	testCases := []struct {
		name string

		cmd        command
		kubeConfig string
		context    string
		path       string
		user       string

		expected []string
	}{
		{
			cmd:      ocApply,
			name:     "apply, no user",
			path:     "/path/to/file",
			expected: []string{"oc", "apply", "-f", "/path/to/file"},
		},
		{
			cmd:      ocApply,
			name:     "apply, user",
			path:     "/path/to/file",
			user:     "joe",
			expected: []string{"oc", "apply", "-f", "/path/to/file", "--as", "joe"},
		},
		{
			cmd:      ocProcess,
			name:     "process, user",
			path:     "/path/to/file",
			user:     "joe",
			expected: []string{"oc", "process", "-f", "/path/to/file", "--as", "joe"},
		},
		{
			cmd:      ocApply,
			name:     "apply, context",
			context:  "/api-build01-ci-devcluster-openshift-com:6443/system:serviceaccount:ci:config-updater",
			path:     "/path/to/file",
			expected: []string{"oc", "apply", "-f", "/path/to/file", "--context", "/api-build01-ci-devcluster-openshift-com:6443/system:serviceaccount:ci:config-updater"},
		},
		{
			cmd:      ocProcess,
			name:     "process, user, context",
			context:  "/context-name",
			path:     "/path/to/file",
			user:     "joe",
			expected: []string{"oc", "process", "-f", "/path/to/file", "--as", "joe", "--context", "/context-name"},
		},
		{
			cmd:        ocProcess,
			name:       "process, user, kubeConfig, context",
			kubeConfig: "/tmp/config",
			context:    "/context-name",
			path:       "/path/to/file",
			user:       "joe",
			expected:   []string{"oc", "process", "-f", "/path/to/file", "--as", "joe", "--kubeconfig", "/tmp/config", "--context", "/context-name"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := makeOcCommand(tc.cmd, tc.kubeConfig, tc.context, tc.path, tc.user)
			if !reflect.DeepEqual(cmd.Args, tc.expected) {
				t.Errorf("Command differs from expected:\n%s", cmp.Diff(tc.expected, cmd.Args))
			}
		})
	}
}

func TestMakeOcApply(t *testing.T) {
	testCases := []struct {
		name string

		kubeConfig string
		context    string
		path       string
		user       string
		dry        bool

		expected []string
	}{
		{
			name:     "no user, not dry",
			path:     "/path/to/file",
			expected: []string{"oc", "apply", "-f", "/path/to/file"},
		},
		{
			name:     "no user, dry",
			path:     "/path/to/different/file",
			dry:      true,
			expected: []string{"oc", "apply", "-f", "/path/to/different/file", "--dry-run"},
		},
		{
			name:     "user, dry",
			path:     "/path/to/file",
			dry:      true,
			user:     "joe",
			expected: []string{"oc", "apply", "-f", "/path/to/file", "--as", "joe", "--dry-run"},
		},
		{
			name:     "user, not dry",
			path:     "/path/to/file",
			user:     "joe",
			expected: []string{"oc", "apply", "-f", "/path/to/file", "--as", "joe"},
		},
		{
			name:     "context, user, not dry",
			context:  "/context-name",
			path:     "/path/to/file",
			user:     "joe",
			expected: []string{"oc", "apply", "-f", "/path/to/file", "--as", "joe", "--context", "/context-name"},
		},
		{
			name:       "kubeConfig, context, user, not dry",
			kubeConfig: "/tmp/config",
			context:    "/context-name",
			path:       "/path/to/file",
			user:       "joe",
			expected:   []string{"oc", "apply", "-f", "/path/to/file", "--as", "joe", "--kubeconfig", "/tmp/config", "--context", "/context-name"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := makeOcApply(tc.kubeConfig, tc.context, tc.path, tc.user, tc.dry)
			if !reflect.DeepEqual(cmd.Args, tc.expected) {
				t.Errorf("Command differs from expected:\n%s", cmp.Diff(tc.expected, cmd.Args))
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

func (m *mockExecutor) getCalls() [][]string {
	var calls [][]string
	for _, call := range m.calls {
		calls = append(calls, call.Args)
	}

	return calls
}

func TestAsGenericManifest(t *testing.T) {
	testCases := []struct {
		description string
		applier     *configApplier
		executions  []error

		expectedCalls [][]string
		expectedError bool
	}{
		{
			description:   "success: oc apply -f path",
			applier:       &configApplier{path: "path"},
			executions:    []error{nil}, // expect a single successful call
			expectedCalls: [][]string{{"oc", "apply", "-f", "path"}},
		},
		{
			description:   "success: oc apply -f path --dry-run",
			applier:       &configApplier{path: "path", dry: true},
			executions:    []error{nil}, // expect a single successful call
			expectedCalls: [][]string{{"oc", "apply", "-f", "path", "--dry-run"}},
		},
		{
			description:   "success: oc apply -f path --dry-run --as user",
			applier:       &configApplier{path: "path", user: "user", dry: true},
			executions:    []error{nil}, // expect a single successful call
			expectedCalls: [][]string{{"oc", "apply", "-f", "path", "--as", "user", "--dry-run"}},
		},
		{
			description:   "failure: oc apply -f path",
			applier:       &configApplier{path: "path"},
			executions:    []error{fmt.Errorf("NOPE")},
			expectedCalls: [][]string{{"oc", "apply", "-f", "path"}},
			expectedError: true,
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
				t.Errorf("calls differ from expected:\n%s", cmp.Diff(tc.expectedCalls, calls))
			}
		})
	}
}

func TestAsTemplate(t *testing.T) {
	testCases := []struct {
		description string
		applier     *configApplier
		executions  []error
		params      []templateapi.Parameter

		expectedCalls [][]string
		expectedError bool
	}{
		{
			description:   "success",
			applier:       &configApplier{path: "path"},
			executions:    []error{nil, nil},
			expectedCalls: [][]string{{"oc", "process", "-f", "path"}, {"oc", "apply", "-f", "-"}},
		},
		{
			description: "success with params",
			applier:     &configApplier{path: "path"},
			executions:  []error{nil, nil},
			params: []templateapi.Parameter{
				{
					Name:        "REDIS_PASSWORD",
					Description: "Password used for Redis authentication",
					Generate:    "expression",
					From:        "[A-Z0-9]{8}",
				},
				{
					Name:        "image",
					Description: "description does not matter",
					Value:       "dockerfile/redis",
				},
				{Name: "name", Description: "description does not matter", Value: "master"},
			},
			expectedCalls: [][]string{{"oc", "process", "-f", "path", "-p", "image=docker.io/redis"}, {"oc", "apply", "-f", "-"}},
		},
		{
			description:   "oc apply fails",
			applier:       &configApplier{path: "path"},
			executions:    []error{nil, fmt.Errorf("REALLY NOPE")},
			expectedCalls: [][]string{{"oc", "process", "-f", "path"}, {"oc", "apply", "-f", "-"}},
			expectedError: true,
		},
		{
			description:   "oc process fails, so no oc apply should not even run",
			applier:       &configApplier{path: "path"},
			executions:    []error{fmt.Errorf("REALLY NOPE EARLIER")},
			expectedCalls: [][]string{{"oc", "process", "-f", "path"}},
			expectedError: true,
		},
	}

	original := os.Getenv("image")
	if err := os.Setenv("image", "docker.io/redis"); err != nil {
		t.Fatalf("failed to set env var 'image', %v", err)
	}
	if os.Getenv("image") != "docker.io/redis" {
		t.Fatalf("failed to set env var 'image'.")
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			executor := &mockExecutor{t: t, responses: tc.executions}
			tc.applier.executor = executor
			err := tc.applier.asTemplate(tc.params)
			if err != nil && !tc.expectedError {
				t.Errorf("returned unexpected error: %v", err)
			}
			if err == nil && tc.expectedError {
				t.Error("expected error, was not returned")
			}

			calls := executor.getCalls()
			if !reflect.DeepEqual(tc.expectedCalls, calls) {
				t.Errorf("calls differ from expected:\n%s", cmp.Diff(tc.expectedCalls, calls))
			}
		})
	}
	if err := os.Setenv("image", original); err != nil {
		t.Fatalf("failed to recover env var 'image', %v", err)
	}
	if os.Getenv("image") != original {
		t.Fatalf("failed to recover env var 'image'.")
	}
}

func TestIsTemplate(t *testing.T) {
	testCases := []struct {
		name           string
		contents       []byte
		expectedParams []templateapi.Parameter
		expected       bool
	}{
		{
			name: "template is a template",
			contents: []byte(`apiVersion: template.openshift.io/v1
kind: Template
metadata:
  name: redis-template
  annotations:
    description: "Description"
    iconClass: "icon-redis"
    tags: "database,nosql"
objects:
- apiVersion: v1
  kind: Pod
  metadata:
    name: redis-master
  spec:
    containers:
    - env:
      - name: REDIS_PASSWORD
        value: ${REDIS_PASSWORD}
      image: ${image}
      name: ${name}
      ports:
      - containerPort: 6379
        protocol: TCP
parameters:
- description: Password used for Redis authentication
  from: '[A-Z0-9]{8}'
  generate: expression
  name: REDIS_PASSWORD
- description: description does not matter
  name: image
  value: dockerfile/redis
- description: description does not matter
  name: name
  value: master
labels:
  redis: master
`),
			expectedParams: []templateapi.Parameter{
				{
					Name:        "REDIS_PASSWORD",
					Description: "Password used for Redis authentication",
					Generate:    "expression",
					From:        "[A-Z0-9]{8}",
				},
				{
					Name:        "image",
					Description: "description does not matter",
					Value:       "dockerfile/redis",
				},
				{Name: "name", Description: "description does not matter", Value: "master"},
			},
			expected: true,
		},
		{
			name: "empty []byte is not a template",
		},
		{
			name:     "english text is not a template",
			contents: []byte("english text is not a template"),
		},
		{
			name: "Route is not a template",
			contents: []byte(`apiVersion: v1
kind: Route
metadata:
	namespace: ci
  name: hook
spec:
  port:
    targetPort: 8888
  path: /hook
  tls:
    insecureEdgeTerminationPolicy: Redirect
    termination: edge
  to:
    kind: Service
    name: hook
`),
		},
		{
			//https://docs.openshift.com/container-platform/4.2/openshift_images/using-templates.html#templates-writing_using-templates
			name: "template in openshift doc in the doc is a template",
			contents: []byte(`apiVersion: v1
kind: Template
metadata:
  name: redis-template
  annotations:
    description: "Description"
    iconClass: "icon-redis"
    tags: "database,nosql"
objects:
- apiVersion: v1
  kind: Pod
  metadata:
    name: redis-master
  spec:
    containers:
    - env:
      - name: REDIS_PASSWORD
        value: ${REDIS_PASSWORD}
      image: dockerfile/redis
      name: master
      ports:
      - containerPort: 6379
        protocol: TCP
parameters:
- description: Password used for Redis authentication
  from: '[A-Z0-9]{8}'
  generate: expression
  name: REDIS_PASSWORD
labels:
  redis: master
`),
			expectedParams: []templateapi.Parameter{
				{
					Name:        "REDIS_PASSWORD",
					Description: "Password used for Redis authentication",
					Generate:    "expression",
					From:        "[A-Z0-9]{8}",
				},
			},
			expected: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			input := bytes.NewBuffer(tc.contents)
			params, is := isTemplate(input)
			if is != tc.expected {
				t.Errorf("%s: expected isTemplate()=%v, got %v", tc.name, tc.expected, is)
			}
			if !reflect.DeepEqual(params, tc.expectedParams) {
				t.Errorf("actual differs from expected:\n%s", cmp.Diff(params, tc.expectedParams))
			}
		})
	}
}
