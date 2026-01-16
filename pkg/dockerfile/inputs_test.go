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
	}{
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
			expected: map[string]api.ImageStreamTagReference{},
		},
		{
			name: "non-registry reference - should be ignored",
			dockerfile: `FROM docker.io/library/golang:1.21
RUN echo "hello"
`,
			expected: map[string]api.ImageStreamTagReference{},
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
			result := DetectInputsFromDockerfile([]byte(tc.dockerfile), tc.existingInputs)

			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("result differs from expected:\n%s", diff)
			}
		})
	}
}
