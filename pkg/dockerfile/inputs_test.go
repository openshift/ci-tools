package dockerfile

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestDetectInputsFromDockerfile(t *testing.T) {
	testCases := []struct {
		name           string
		dockerfile     string
		existingInputs map[string]api.ImageBuildInputs
		expected       map[string]api.ImageStreamTagReference
		expectError    bool
	}{
		{
			name:       "empty dockerfile",
			dockerfile: "",
			expected:   nil,
		},
		{
			name: "single registry reference",
			dockerfile: `FROM registry.ci.openshift.org/ocp/4.19:base
RUN echo "hello"
`,
			expected: map[string]api.ImageStreamTagReference{
				"ocp_4.19_base": {
					Namespace: "ocp",
					Name:      "4.19",
					Tag:       "base",
					As:        "registry.ci.openshift.org/ocp/4.19:base",
				},
			},
		},
		{
			name: "multiple registry references",
			dockerfile: `FROM registry.ci.openshift.org/ocp/4.19:base AS builder
COPY --from=registry.ci.openshift.org/ocp/4.19:tools /usr/bin/tool /usr/bin/
RUN echo "building"
`,
			expected: map[string]api.ImageStreamTagReference{
				"ocp_4.19_base": {
					Namespace: "ocp",
					Name:      "4.19",
					Tag:       "base",
					As:        "registry.ci.openshift.org/ocp/4.19:base",
				},
				"ocp_4.19_tools": {
					Namespace: "ocp",
					Name:      "4.19",
					Tag:       "tools",
					As:        "registry.ci.openshift.org/ocp/4.19:tools",
				},
			},
		},
		{
			name: "quay-proxy registry reference with normal tag",
			dockerfile: `FROM quay-proxy.ci.openshift.org/openshift/release:golang-1.21
RUN echo "hello"
`,
			expected: map[string]api.ImageStreamTagReference{
				"openshift_release_golang-1.21": {
					Namespace: "openshift",
					Name:      "release",
					Tag:       "golang-1.21",
					As:        "quay-proxy.ci.openshift.org/openshift/release:golang-1.21",
				},
			},
		},
		{
			name: "quay-proxy registry reference with encoded tag",
			dockerfile: `FROM quay-proxy.ci.openshift.org/openshift/ci:ocp_builder_rhel-9-golang-1.21-openshift-4.16
RUN echo "hello"
`,
			expected: map[string]api.ImageStreamTagReference{
				"ocp_builder_rhel-9-golang-1.21-openshift-4.16": {
					Namespace: "ocp",
					Name:      "builder",
					Tag:       "rhel-9-golang-1.21-openshift-4.16",
					As:        "quay-proxy.ci.openshift.org/openshift/ci:ocp_builder_rhel-9-golang-1.21-openshift-4.16",
				},
			},
		},
		{
			name: "registry.svc.ci.openshift.org reference",
			dockerfile: `FROM registry.svc.ci.openshift.org/ocp/builder:rhel-9-golang-1.21-openshift-4.16
RUN echo "hello"
`,
			expected: map[string]api.ImageStreamTagReference{
				"ocp_builder_rhel-9-golang-1.21-openshift-4.16": {
					Namespace: "ocp",
					Name:      "builder",
					Tag:       "rhel-9-golang-1.21-openshift-4.16",
					As:        "registry.svc.ci.openshift.org/ocp/builder:rhel-9-golang-1.21-openshift-4.16",
				},
			},
		},
		{
			name: "skip manual replacement",
			dockerfile: `FROM registry.ci.openshift.org/ocp/4.19:base
RUN echo "hello"
`,
			existingInputs: map[string]api.ImageBuildInputs{
				"custom_base": {
					As: []string{"registry.ci.openshift.org/ocp/4.19:base"},
				},
			},
			expected: nil, // Should be nil because manual inputs exist
		},
		{
			name: "non-registry reference - should be ignored",
			dockerfile: `FROM docker.io/library/golang:1.21
RUN echo "hello"
`,
			expected: nil,
		},
		{
			name: "mixed references - only registry ones detected",
			dockerfile: `FROM registry.ci.openshift.org/ocp/4.19:base AS builder
FROM docker.io/library/alpine:latest
COPY --from=builder /app /app
`,
			expected: map[string]api.ImageStreamTagReference{
				"ocp_4.19_base": {
					Namespace: "ocp",
					Name:      "4.19",
					Tag:       "base",
					As:        "registry.ci.openshift.org/ocp/4.19:base",
				},
			},
		},
		{
			name: "duplicate references - only one entry",
			dockerfile: `FROM registry.ci.openshift.org/ocp/4.19:base AS builder
FROM registry.ci.openshift.org/ocp/4.19:base AS runtime
`,
			expected: map[string]api.ImageStreamTagReference{
				"ocp_4.19_base": {
					Namespace: "ocp",
					Name:      "4.19",
					Tag:       "base",
					As:        "registry.ci.openshift.org/ocp/4.19:base",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := DetectInputsFromDockerfile([]byte(tc.dockerfile), tc.existingInputs)

			if tc.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("result differs from expected:\n%s", diff)
			}
		})
	}
}

func TestOrgRepoTagFromPullString(t *testing.T) {
	testCases := []struct {
		name        string
		pullString  string
		expected    orgRepoTag
		expectError bool
	}{
		{
			name:       "full reference with tag",
			pullString: "registry.ci.openshift.org/ocp/4.19:base",
			expected: orgRepoTag{
				Org:  "ocp",
				Repo: "4.19",
				Tag:  "base",
			},
		},
		{
			name:        "wrong reguistry format",
			pullString:  "registry.ci.openshift.org/ocp:latest",
			expected:    orgRepoTag{},
			expectError: true,
		},
		{
			name:       "quay-proxy reference with encoded tag",
			pullString: "quay-proxy.ci.openshift.org/openshift/ci:ocp_builder_rhel-9-golang-1.21-openshift-4.16",
			expected: orgRepoTag{
				Org:  "ocp",
				Repo: "builder",
				Tag:  "rhel-9-golang-1.21-openshift-4.16",
			},
		},
		{
			name:        "wrong quay registry format",
			pullString:  "quay-proxy.ci.openshift.org/openshift/ci:latest",
			expected:    orgRepoTag{},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := orgRepoTagFromPullString(tc.pullString)

			if tc.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("result differs from expected:\n%s", diff)
			}
		})
	}
}

func TestExtractReplacementCandidatesFromDockerfile(t *testing.T) {
	testCases := []struct {
		name        string
		dockerfile  string
		expected    []string
		expectError bool
	}{
		{
			name: "simple FROM",
			dockerfile: `FROM registry.ci.openshift.org/ocp/4.19:base
RUN echo "hello"
`,
			expected: []string{"registry.ci.openshift.org/ocp/4.19:base"},
		},
		{
			name: "multi-stage with COPY --from",
			dockerfile: `FROM registry.ci.openshift.org/ocp/4.19:base AS builder
RUN make build
FROM registry.ci.openshift.org/ocp/4.19:minimal
COPY --from=builder /app /app
`,
			expected: []string{
				"registry.ci.openshift.org/ocp/4.19:base",
				"registry.ci.openshift.org/ocp/4.19:minimal",
				"builder", // stage name is also collected
			},
		},
		{
			name: "COPY --from with stage name",
			dockerfile: `FROM golang:1.21 AS builder
RUN make build
FROM alpine:latest
COPY --from=builder /app /app
`,
			expected: []string{
				"golang:1.21",
				"alpine:latest",
				"builder", // stage name is also collected
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := extractReplacementCandidatesFromDockerfile([]byte(tc.dockerfile))

			if tc.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if err == nil {
				// Convert set to slice for comparison
				resultSlice := result.UnsortedList()
				if len(resultSlice) != len(tc.expected) {
					t.Errorf("expected %d candidates, got %d", len(tc.expected), len(resultSlice))
				}
				for _, exp := range tc.expected {
					if !result.Has(exp) {
						t.Errorf("expected candidate %q not found in result", exp)
					}
				}
			}
		})
	}
}
