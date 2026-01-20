package dockerfile

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestOrgRepoTagFromPullString(t *testing.T) {
	testCases := []struct {
		name        string
		pullString  string
		expected    OrgRepoTag
		expectError bool
	}{
		{
			name:       "single component (repo only)",
			pullString: "redis",
			expected:   OrgRepoTag{Org: "_", Repo: "redis", Tag: "latest"},
		},
		{
			name:       "single component with tag",
			pullString: "redis:6.0",
			expected:   OrgRepoTag{Org: "_", Repo: "redis", Tag: "6.0"},
		},
		{
			name:       "two components (org/repo)",
			pullString: "library/redis",
			expected:   OrgRepoTag{Org: "library", Repo: "redis", Tag: "latest"},
		},
		{
			name:       "two components with tag",
			pullString: "library/redis:6.0",
			expected:   OrgRepoTag{Org: "library", Repo: "redis", Tag: "6.0"},
		},
		{
			name:       "three components (registry/org/repo)",
			pullString: "docker.io/library/redis",
			expected:   OrgRepoTag{Org: "library", Repo: "redis", Tag: "latest"},
		},
		{
			name:       "three components with tag",
			pullString: "docker.io/library/redis:6.0",
			expected:   OrgRepoTag{Org: "library", Repo: "redis", Tag: "6.0"},
		},
		{
			name:       "four components (the failing case from the error)",
			pullString: "quay.io/redhat-services-prod/openshift/boilerplate",
			expected:   OrgRepoTag{Org: "openshift", Repo: "boilerplate", Tag: "latest"},
		},
		{
			name:       "four components with tag",
			pullString: "quay.io/redhat-services-prod/openshift/boilerplate:image-v7.4.0",
			expected:   OrgRepoTag{Org: "openshift", Repo: "boilerplate", Tag: "image-v7.4.0"},
		},
		{
			name:       "five components (deeply nested)",
			pullString: "registry.com/team/project/subproject/service/image",
			expected:   OrgRepoTag{Org: "service", Repo: "image", Tag: "latest"},
		},
		{
			name:       "five components with tag",
			pullString: "registry.com/team/project/subproject/service/image:v1.2.3",
			expected:   OrgRepoTag{Org: "service", Repo: "image", Tag: "v1.2.3"},
		},
		{
			name:       "registry.svc.ci.openshift.org example",
			pullString: "registry.svc.ci.openshift.org/ocp/builder:rhel-8-golang-1.15",
			expected:   OrgRepoTag{Org: "ocp", Repo: "builder", Tag: "rhel-8-golang-1.15"},
		},
		{
			name:       "full reference with tag",
			pullString: "registry.ci.openshift.org/ocp/4.19:base",
			expected: OrgRepoTag{
				Org:  "ocp",
				Repo: "4.19",
				Tag:  "base",
			},
		},
		{
			name:       "quay-proxy reference with encoded tag",
			pullString: "quay-proxy.ci.openshift.org/openshift/ci:ocp_builder_rhel-9-golang-1.21-openshift-4.16",
			expected: OrgRepoTag{
				Org:  "ocp",
				Repo: "builder",
				Tag:  "rhel-9-golang-1.21-openshift-4.16",
			},
		},
		{
			name:        "wrong quay registry format",
			pullString:  "quay-proxy.ci.openshift.org/openshift/ci:latest",
			expected:    OrgRepoTag{},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := OrgRepoTagFromPullString(tc.pullString)

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
