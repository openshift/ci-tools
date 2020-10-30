package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/logrusutil"

	templateapi "github.com/openshift/api/template/v1"
)

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
	defer func() {
		if err := os.Setenv("image", original); err != nil {
			t.Fatalf("failed to recover env var 'image', %v", err)
		}
		if os.Getenv("image") != original {
			t.Fatalf("failed to recover env var 'image'.")
		}
	}()
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

type fakeExecutor struct {
}

func (f *fakeExecutor) runAndCheck(cmd *exec.Cmd, _ string) ([]byte, error) {
	return nil, nil
}

func TestCensoringFormatter(t *testing.T) {
	envVars := map[string]string{
		"test_env_var_1": "SECRET",
		"test_env_var_2": "MYSTERY",
	}

	originalEnvVars := make(map[string]string)

	for k := range envVars {
		originalEnvVars[k] = os.Getenv(k)
	}

	for k, v := range envVars {
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("failed to set env var 'image', %v", err)
		}
	}

	defer func() {
		for k, v := range originalEnvVars {
			if err := os.Setenv(k, v); err != nil {
				t.Fatalf("failed to recover env var 'image', %v", err)
			}
			if os.Getenv(k) != v {
				t.Fatalf("failed to recover env var 'image'.")
			}
		}
	}()

	testCases := []struct {
		description string
		entry       *logrus.Entry
		expected    string
	}{
		{
			description: "all occurrences of a single secret in a message are censored",
			entry:       &logrus.Entry{Message: "A SECRET is a SECRET if it is secret"},
			expected:    "level=panic msg=\"A CENSORED is a CENSORED if it is secret\"\n",
		},
		{
			description: "no occurrences of a non-secret in a message are censored",
			entry:       &logrus.Entry{Message: "A test_env_var_0 is a test_env_var_0 if it is NOT secret"},
			expected:    "level=panic msg=\"A test_env_var_0 is a test_env_var_0 if it is NOT secret\"\n",
		},
		{
			description: "occurrences of a multiple secrets in a message are censored",
			entry:       &logrus.Entry{Message: "A SECRET is a MYSTERY"},
			expected:    "level=panic msg=\"A CENSORED is a CENSORED\"\n",
		},
		{
			description: "occurrences of multiple secrets in a field",
			entry:       &logrus.Entry{Message: "message", Data: logrus.Fields{"key": "A SECRET is a MYSTERY"}},
			expected:    "level=panic msg=message key=\"A CENSORED is a CENSORED\"\n",
		},
		{
			description: "occurrences of a secret in a non-string field",
			entry:       &logrus.Entry{Message: "message", Data: logrus.Fields{"key": fmt.Errorf("A SECRET is a MYSTERY")}},
			expected:    "level=panic msg=message key=\"A CENSORED is a CENSORED\"\n",
		},
	}

	baseFormatter := &logrus.TextFormatter{
		DisableColors:    true,
		DisableTimestamp: true,
	}
	secrets = &secretGetter{secrets: sets.NewString()}
	formatter := logrusutil.NewCensoringFormatter(baseFormatter, secrets.getSecrets)
	logrus.SetFormatter(formatter)
	applier := &configApplier{path: "path", user: "user", dry: true, executor: &fakeExecutor{}}
	err := applier.asTemplate([]templateapi.Parameter{
		{
			Name:  "test_env_var_0",
			Value: "test_env_var_0",
		},
		{
			Name:  "test_env_var_1",
			Value: "value does not matter",
		},
		{
			Name:  "test_env_var_2",
			Value: "value does not matter",
		},
	})
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			censored, err := formatter.Format(tc.entry)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if string(censored) != tc.expected {
				t.Errorf("Expected '%s', got '%s'", tc.expected, string(censored))
			}
		})
	}
}

type fileStat struct {
	name  string
	isDir bool
}

func (fs *fileStat) Name() string       { return fs.name }
func (fs *fileStat) IsDir() bool        { return fs.isDir }
func (fs *fileStat) Size() int64        { panic("not implemented") }
func (fs *fileStat) Mode() os.FileMode  { panic("not implemented") }
func (fs *fileStat) ModTime() time.Time { panic("not implemented") }
func (fs *fileStat) Sys() interface{}   { panic("not implemented") }

func TestFileFilter(t *testing.T) {
	testCases := []struct {
		name       string
		file       *fileStat
		expectSkip bool
		expectErr  error
	}{
		{
			name:       "dir is skipped",
			file:       &fileStat{isDir: true},
			expectSkip: true,
		},
		{
			name:      "underscore dir yields filepath.SkipDir",
			file:      &fileStat{name: "_dir", isDir: true},
			expectErr: filepath.SkipDir,
		},
		{
			name:       "not yaml is skipped",
			file:       &fileStat{name: "readme.md"},
			expectSkip: true,
		},
		{
			name:       "underscore file is skipped",
			file:       &fileStat{name: "_some_file.yaml"},
			expectSkip: true,
		},
		{
			name: "yaml is not skipped",
			file: &fileStat{name: "manifest.yaml"},
		},
	}

	for _, tc := range testCases {
		skip, err := fileFilter(tc.file, "")
		if err != tc.expectErr {
			t.Errorf("expect err: %v, got err: %v", tc.expectErr, err)
		}
		if skip != tc.expectSkip {
			t.Errorf("expect skip: %t, got skip: %t", tc.expectSkip, skip)
		}
	}
}
