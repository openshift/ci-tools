package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/flagutil"

	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-tools/pkg/secrets"
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
		dry        dryRunMethod
		apply      applyMethod

		expected []string
	}{
		{
			name:     "no user, not dry",
			path:     "/path/to/file",
			expected: []string{"oc", "apply", "-f", "/path/to/file", "-o", "name"},
		},
		{
			name:     "no user, client dry",
			path:     "/path/to/different/file",
			dry:      dryClient,
			expected: []string{"oc", "apply", "-f", "/path/to/different/file", "-o", "name", "--dry-run=client"},
		},
		{
			name:     "no user, server dry",
			path:     "/path/to/different/file",
			dry:      dryServer,
			expected: []string{"oc", "apply", "-f", "/path/to/different/file", "-o", "name", "--dry-run=server", "--validate=true"},
		},
		{
			name:     "no user, auto dry means server dry",
			path:     "/path/to/different/file",
			dry:      dryServer,
			expected: []string{"oc", "apply", "-f", "/path/to/different/file", "-o", "name", "--dry-run=server", "--validate=true"},
		},
		{
			name:     "user, client dry",
			path:     "/path/to/file",
			dry:      dryClient,
			user:     "joe",
			expected: []string{"oc", "apply", "-f", "/path/to/file", "-o", "name", "--as", "joe", "--dry-run=client"},
		},
		{
			name:     "user, not dry",
			path:     "/path/to/file",
			user:     "joe",
			expected: []string{"oc", "apply", "-f", "/path/to/file", "-o", "name", "--as", "joe"},
		},
		{
			name:     "context, user, not dry",
			context:  "/context-name",
			path:     "/path/to/file",
			user:     "joe",
			expected: []string{"oc", "apply", "-f", "/path/to/file", "-o", "name", "--as", "joe", "--context", "/context-name"},
		},
		{
			name:       "kubeConfig, context, user, not dry",
			kubeConfig: "/tmp/config",
			context:    "/context-name",
			path:       "/path/to/file",
			user:       "joe",
			expected:   []string{"oc", "apply", "-f", "/path/to/file", "-o", "name", "--as", "joe", "--kubeconfig", "/tmp/config", "--context", "/context-name"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := makeOcApply(tc.kubeConfig, tc.context, tc.path, tc.user, tc.dry, tc.apply)
			if diff := cmp.Diff(tc.expected, cmd.Args); diff != "" {
				t.Errorf("Command differs from expected:\n%s", diff)
			}
		})
	}
}

func TestInferMissingNamespaces(t *testing.T) {
	testcases := []struct {
		description   string
		ocApplyOutput []byte
		expected      sets.Set[string]
	}{
		{
			description:   "single line of interest",
			ocApplyOutput: []byte(`Error from server (NotFound): namespaces "this-is-missing" not found`),
			expected:      sets.New[string]("this-is-missing"),
		},
		{
			description: "single line of interest between some clutter",
			ocApplyOutput: []byte(`a line of clutter
Error from server (NotFound): namespaces "this-is-missing" not found
another line of clutter`),
			expected: sets.New[string]("this-is-missing"),
		},
		{
			description: "multiple lines of interest in clutter",
			ocApplyOutput: []byte(`a line of clutter
Error from server (NotFound): namespaces "this-is-missing" not found
another line of clutter
Error from server (NotFound): namespaces "this-is-missing-too" not found
`),
			expected: sets.New[string]("this-is-missing", "this-is-missing-too"),
		},
		{
			description:   "Message with multiple error levels",
			ocApplyOutput: []byte(`Error from server (NotFound): error when creating "clusters/app.ci/prometheus-access/managed-services/admin_rbac.yaml": namespaces "managed-services" not found`),
			expected:      sets.New[string]("managed-services"),
		},
	}
	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			namespaces := inferMissingNamespaces(tc.ocApplyOutput)
			if diff := cmp.Diff(tc.expected, namespaces); diff != "" {
				t.Errorf("Detected missing namespaces differ:\n%s", diff)
			}
		})
	}
}

type response struct {
	output []byte
	err    error
}

type mockExecutor struct {
	t *testing.T

	calls     []*exec.Cmd
	responses []response
}

func (m *mockExecutor) runAndCheck(cmd *exec.Cmd, _ string) ([]byte, error) {
	responseIdx := len(m.calls)
	m.calls = append(m.calls, cmd)

	if len(m.responses) < responseIdx+1 {
		m.t.Fatalf("mockExecutor received unexpected call: %v", cmd.Args)
	}
	return m.responses[responseIdx].output, m.responses[responseIdx].err
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
		executions  []response

		expectedCalls      [][]string
		expectedNamespaces namespaceActions
		expectedError      bool
	}{
		{
			description:   "success: oc apply -f path",
			applier:       &configApplier{path: "path"},
			executions:    []response{{err: nil}}, // expect a single successful call
			expectedCalls: [][]string{{"oc", "apply", "-f", "path", "-o", "name"}},
		},
		{
			description:   "success: oc apply -f path --dry-run",
			applier:       &configApplier{path: "path", dry: dryClient},
			executions:    []response{{err: nil}}, // expect a single successful call
			expectedCalls: [][]string{{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=client"}},
		},
		{
			description:   "success: oc apply -f path --dry-run --as user",
			applier:       &configApplier{path: "path", user: "user", dry: dryClient},
			executions:    []response{{err: nil}}, // expect a single successful call
			expectedCalls: [][]string{{"oc", "apply", "-f", "path", "-o", "name", "--as", "user", "--dry-run=client"}},
		},
		{
			description:   "failure: oc apply -f path",
			applier:       &configApplier{path: "path"},
			executions:    []response{{err: fmt.Errorf("NOPE")}},
			expectedCalls: [][]string{{"oc", "apply", "-f", "path", "-o", "name"}},
			expectedError: true,
		},
		{
			description:        "success: server-side dry run records which namespaces would be created",
			applier:            &configApplier{path: "path", dry: dryServer},
			executions:         []response{{output: []byte("namespace/to-be-created"), err: nil}}, // expect a single successful call
			expectedCalls:      [][]string{{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=server", "--validate=true"}},
			expectedNamespaces: namespaceActions{Created: sets.New[string]("to-be-created")},
		},
		{
			description: "success: server-side dry recovers missing namespaces by creating them",
			applier:     &configApplier{path: "path", dry: dryServer},
			executions: []response{
				{output: []byte("Error from server (NotFound): namespaces \"this-is-missing\" not found"), err: errors.New("NOPE")},
				{err: nil},
				{err: nil},
			}, // expect a single successful call
			expectedCalls: [][]string{
				{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=server", "--validate=true"},
				{"oc", "apply", "-f", "-", "-o", "name"},
				{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=server", "--validate=true"},
			},
			expectedNamespaces: namespaceActions{Assumed: sets.New[string]("this-is-missing")},
		},
		{
			description: "success: server-side dry recovers multiple missing namespaces by creating them",
			applier:     &configApplier{path: "path", dry: dryServer},
			executions: []response{
				{
					output: []byte("Error from server (NotFound): namespaces \"missing-1\" not found\nError from server (NotFound): namespaces \"missing-2\" not found\n"),
					err:    errors.New("NOPE"),
				},
				{err: nil},
				{err: nil},
				{
					output: []byte("Error from server (NotFound): namespaces \"missing-3\" not found"),
					err:    errors.New("NOPE"),
				},
				{err: nil},
				{err: nil},
			}, // expect a single successful call
			expectedCalls: [][]string{
				{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=server", "--validate=true"}, // Fails because two namespaces are missing
				{"oc", "apply", "-f", "-", "-o", "name"},                                           // Create one namespace
				{"oc", "apply", "-f", "-", "-o", "name"},                                           // Create second namespace
				{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=server", "--validate=true"}, // Fails because a third namespace is missing
				{"oc", "apply", "-f", "-", "-o", "name"},                                           // Create third namespace
				{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=server", "--validate=true"}, // Succeeds
			},
			expectedNamespaces: namespaceActions{Assumed: sets.New[string]("missing-1", "missing-2", "missing-3")},
		},
		{
			description: "failure: server-side dry does not try to recover from unrelated failures",
			applier:     &configApplier{path: "path", dry: dryServer},
			executions: []response{
				{output: []byte("Error from server (BuggerOff): I hate these manifests"), err: errors.New("NOPE")},
			}, // expect a single successful call
			expectedCalls: [][]string{
				{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=server", "--validate=true"},
			},
			expectedError: true,
		},
		{
			description: "success: server-side dry recovers missing namespaces by creating them, records if they would be created",
			applier:     &configApplier{path: "path", dry: dryServer},
			executions: []response{
				{output: []byte("Error from server (NotFound): namespaces \"this-is-missing\" not found`"), err: errors.New("NOPE")},
				{err: nil},
				{output: []byte("namespace/this-is-missing"), err: nil},
			}, // expect a single successful call
			expectedCalls: [][]string{
				{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=server", "--validate=true"},
				{"oc", "apply", "-f", "-", "-o", "name"},
				{"oc", "apply", "-f", "path", "-o", "name", "--dry-run=server", "--validate=true"},
			},
			expectedNamespaces: namespaceActions{
				Created: sets.New[string]("this-is-missing"),
				Assumed: sets.New[string]("this-is-missing"),
			},
		},
		{
			description: "success: file with name starts with '_SS' is applied with --server-side",
			applier:     &configApplier{path: "SS_path", dry: dryNone},
			executions:  []response{{err: nil}},
			expectedCalls: [][]string{
				{"oc", "apply", "-f", "SS_path", "-o", "name", "--server-side=true"},
			},
		},
		{
			description: "success: enable server-side apply",
			applier:     &configApplier{path: "path", dry: dryNone, apply: applyServer},
			executions:  []response{{err: nil}},
			expectedCalls: [][]string{
				{"oc", "apply", "-f", "path", "-o", "name", "--server-side=true"},
			},
		},
		{
			description: "success: no duplicated --server-side flag",
			applier:     &configApplier{path: "_SS_path", dry: dryNone, apply: applyServer},
			executions:  []response{{err: nil}},
			expectedCalls: [][]string{
				{"oc", "apply", "-f", "_SS_path", "-o", "name", "--server-side=true"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			executor := &mockExecutor{t: t, responses: tc.executions}
			tc.applier.executor = executor
			namespaces, err := tc.applier.asGenericManifest()
			if err != nil && !tc.expectedError {
				t.Errorf("returned unexpected error: %v", err)
			}
			if err == nil && tc.expectedError {
				t.Error("expected error, was not returned")
			}

			if diff := cmp.Diff(tc.expectedNamespaces, namespaces); diff != "" {
				t.Errorf("Namespace actions differ from expected:\n%s", diff)
			}

			calls := executor.getCalls()
			if diff := cmp.Diff(tc.expectedCalls, calls); diff != "" {
				t.Errorf("calls differ from expected:\n%s", diff)
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

		expectedCalls      [][]string
		expectedNamespaces namespaceActions
		expectedError      bool
	}{
		{
			description:   "success",
			applier:       &configApplier{path: "path"},
			executions:    []error{nil, nil},
			expectedCalls: [][]string{{"oc", "process", "-f", "path"}, {"oc", "apply", "-f", "-", "-o", "name"}},
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
			expectedCalls: [][]string{{"oc", "process", "-f", "path", "-p", "image=docker.io/redis"}, {"oc", "apply", "-f", "-", "-o", "name"}},
		},
		{
			description:   "oc apply fails",
			applier:       &configApplier{path: "path"},
			executions:    []error{nil, fmt.Errorf("REALLY NOPE")},
			expectedCalls: [][]string{{"oc", "process", "-f", "path"}, {"oc", "apply", "-f", "-", "-o", "name"}},
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
			var responses []response
			for _, err := range tc.executions {
				responses = append(responses, response{err: err})
			}
			executor := &mockExecutor{t: t, responses: responses}
			censor := secrets.NewDynamicCensor()
			tc.applier.executor = executor
			tc.applier.censor = &censor
			namespaces, err := tc.applier.asTemplate(tc.params)
			if err != nil && !tc.expectedError {
				t.Errorf("returned unexpected error: %v", err)
			}
			if err == nil && tc.expectedError {
				t.Error("expected error, was not returned")
			}
			if diff := cmp.Diff(tc.expectedNamespaces, namespaces); diff != "" {
				t.Errorf("Namespace actions differ from expected:\n%s", diff)
			}

			calls := executor.getCalls()
			if diff := cmp.Diff(tc.expectedCalls, calls); diff != "" {
				t.Errorf("calls differ from expected:\n%s", diff)
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
		name        string
		file        *fileStat
		path        string
		ignoreFiles flagutil.Strings
		expectSkip  bool
		expectErr   error
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
		{
			name:        "ignored files are skipped",
			file:        &fileStat{name: "cert-manager.yaml"},
			path:        "clusters/build-clusters/common/cert-manager.yaml",
			ignoreFiles: flagutil.NewStrings("clusters/build-clusters/common/cert-manager.yaml"),
			expectSkip:  true,
		},
	}

	for _, tc := range testCases {
		skip, err := fileFilter(tc.file, tc.path, tc.ignoreFiles)
		if err != tc.expectErr {
			t.Errorf("expect err: %v, got err: %v", tc.expectErr, err)
		}
		if skip != tc.expectSkip {
			t.Errorf("expect skip: %t, got skip: %t", tc.expectSkip, skip)
		}
	}
}

func TestSelectDryRun(t *testing.T) {
	testCases := []struct {
		description      string
		openshiftVersion string
		expected         dryRunMethod
	}{
		{
			description: "no openshift version -> client",
			expected:    dryClient,
		},
		{
			description:      "openshift under 4.5 -> client",
			openshiftVersion: "4.4.9",
			expected:         dryClient,
		},
		{
			description:      "openshift at least 4.5 -> server",
			openshiftVersion: "4.5.0",
			expected:         dryServer,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			if method := selectDryRun(ocVersionOutput{Openshift: tc.openshiftVersion}); method != tc.expected {
				t.Errorf("Expected dry run method %s, got %s", tc.expected, method)
			}
		})
	}
}

func TestExtractNamespaces(t *testing.T) {
	testcases := []struct {
		description string
		applyOutput []byte
		expected    sets.Set[string]
	}{
		{
			description: "empty input",
			expected:    sets.Set[string]{},
		},
		{
			description: "single line",
			applyOutput: []byte("namespace/ns-name"),
			expected:    sets.New[string]("ns-name"),
		},
		{
			description: "multiple lines separated by clutter",
			applyOutput: []byte("namespace/ns-name\n\n\ncluttter\nmorecluttter\nevenmorecluttern\nnamespaceclutter\nnamespace/another-ns-name"),
			expected:    sets.New[string]("ns-name", "another-ns-name"),
		},
	}
	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			nss := extractNamespaces(tc.applyOutput)
			if diff := cmp.Diff(tc.expected, nss); diff != "" {
				t.Errorf("Extracted namespace names differ from expected:\n%s", diff)
			}
		})
	}
}
