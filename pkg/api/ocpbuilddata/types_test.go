package ocpbuilddata

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"
)

func TestOCPImageConfig(t *testing.T) {
	testcases := []struct {
		name           string
		in             []byte
		expectedGitURL string
		expectedStream string
		expectedName   string
	}{
		{
			name: "simple",
			in: []byte(`content:
  source:
    dockerfile: Dockerfile.openshift
    git:
      branch:
        target: release-{MAJOR}.{MINOR}
      url: git@github.com:openshift/whereabouts-cni.git
distgit:
  branch: rhaos-{MAJOR}.{MINOR}-rhel-7
from:
  builder:
  - stream: rhel-8-golang
  - stream: golang
  stream: rhel
name: openshift/ose-multus-whereabouts-ipam-cni
owners:
- multus-dev@redhat.com`),
			expectedGitURL: "git@github.com:openshift/whereabouts-cni.git",
			expectedStream: "rhel",
			expectedName:   "openshift/ose-multus-whereabouts-ipam-cni",
		},
		{
			name: "complex",
			in: []byte(`container_yaml:
  go:
    modules:
    - module: k8s.io/autoscaler
content:
  source:
    dockerfile: images/cluster-autoscaler/Dockerfile.rhel7
    git:
      branch:
        target: release-{MAJOR}.{MINOR}
      url: git@github.com:openshift-priv/kubernetes-autoscaler.git
distgit:
  namespace: containers
enabled_repos:
- rhel-8-baseos-rpms
- rhel-8-appstream-rpms
- rhel-8-server-ose-rpms
from:
  builder:
  - stream: golang
  stream: rhel
labels:
  License: ASL 2.0
  io.k8s.description: Cluster Autoscaler for OpenShift and Kubernetes.
  io.k8s.display-name: OpenShift Container Platform Cluster Autoscaler
  io.openshift.tags: openshift,autoscaler
  vendor: Red Hat
name: openshift/ose-cluster-autoscaler
owners:
- avagarwa@redhat.com
`),
			expectedGitURL: "git@github.com:openshift-priv/kubernetes-autoscaler.git",
			expectedStream: "rhel",
			expectedName:   "openshift/ose-cluster-autoscaler",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			ocpImageConfig := &OCPImageConfig{}
			if err := yaml.Unmarshal(tc.in, ocpImageConfig); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			if ocpImageConfig.Content.Source.Git.URL != tc.expectedGitURL {
				t.Errorf("expected ocpImageConfig.Content.Source.Git.URL to be %s, was %s", tc.expectedGitURL, ocpImageConfig.Content.Source.Git.URL)
			}

			if ocpImageConfig.From.Stream != tc.expectedStream {
				t.Errorf("expected ocpImageConfig.From.Stream to be %s, was %s", tc.expectedStream, ocpImageConfig.From.Stream)
			}
			if ocpImageConfig.Name != tc.expectedName {
				t.Errorf("expected name to be %s, was %s", tc.expectedName, ocpImageConfig.Name)
			}
		})

	}
}

func TestSetPublicRepo(t *testing.T) {
	testCases := []struct {
		name      string
		orgRepoIn string
		mappings  []PublicPrivateMapping

		expected OrgRepo
	}{
		{
			name:      "no match, original string is returned",
			orgRepoIn: "kubeflow/kubeflow",
			mappings: []PublicPrivateMapping{
				{Private: "https://github.com/openshift-priv", Public: "https://github.com/openshift"},
				{Private: "https://github.com/openshift/ose", Public: "https://github.com/openshift/origin"},
			},
			expected: OrgRepo{Org: "kubeflow", Repo: "kubeflow"},
		},
		{
			name:      "single match is used",
			orgRepoIn: "kubeflow-priv/kubeflow",
			mappings: []PublicPrivateMapping{
				{Private: "https://github.com/kubeflow-priv", Public: "https://github.com/kubeflow"},
				{Private: "https://github.com/openshift/ose", Public: "https://github.com/openshift/origin"},
			},
			expected: OrgRepo{Org: "kubeflow", Repo: "kubeflow"},
		},
		{
			name:      "multiple matches, longest prefix match is used",
			orgRepoIn: "kubeflow-priv/kubeflow",
			mappings: []PublicPrivateMapping{
				{Private: "https://github.com/kubeflow-priv", Public: "https://github.com/kubeflow"},
				{Private: "https://github.com/kubeflow-priv/kubeflow", Public: "https://github.com/openshift/origin"},
			},
			expected: OrgRepo{Org: "openshift", Repo: "origin"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &OCPImageConfig{Name: tc.orgRepoIn}
			cfg.setPublicOrgRepo(tc.mappings)
			if diff := cmp.Diff(cfg.PublicRepo, tc.expected); diff != "" {
				t.Errorf("actual differs from expected: %s", diff)
			}
		})
	}
}

func TestDereferenceConfig(t *testing.T) {
	testCases := []struct {
		name           string
		config         OCPImageConfig
		majorMinor     MajorMinor
		allConfigs     map[string]OCPImageConfig
		streamMap      StreamMap
		groupYAML      GroupYAML
		expectedConfig OCPImageConfig
		expectedError  error
	}{
		{
			name: "config.from.stream gets replaced",
			config: OCPImageConfig{
				From: OCPImageConfigFrom{
					OCPImageConfigFromStream: OCPImageConfigFromStream{Stream: "golang"},
				},
			},
			streamMap: StreamMap{"golang": {UpstreamImage: "openshift/golang-builder:rhel_8_golang_1.14"}},
			expectedConfig: OCPImageConfig{
				From: OCPImageConfigFrom{
					OCPImageConfigFromStream: OCPImageConfigFromStream{Stream: "openshift/golang-builder:rhel_8_golang_1.14"},
				},
			},
		},
		{
			name: "config.from.member gets replaced",
			config: OCPImageConfig{
				From: OCPImageConfigFrom{
					OCPImageConfigFromStream: OCPImageConfigFromStream{Member: "openshift-enterprise-base"},
				},
				Version: MajorMinor{Major: "4", Minor: "6"},
			},
			allConfigs: map[string]OCPImageConfig{
				"images/openshift-enterprise-base.yml": {
					Name:    "openshift/ose-base",
					Version: MajorMinor{Major: "4", Minor: "6"},
				},
			},
			expectedConfig: OCPImageConfig{
				From: OCPImageConfigFrom{
					OCPImageConfigFromStream: OCPImageConfigFromStream{
						Stream: "registry.svc.ci.openshift.org/ocp/4.6:base"},
				},
			},
		},
		{
			name:          "both config from.stream and config.from.member are empty, error",
			expectedError: errors.New("failed to find replacement for .from.stream"),
		},
		{
			name: "config.from.builder.stream gets replaced",
			config: OCPImageConfig{
				From: OCPImageConfigFrom{
					Builder:                  []OCPImageConfigFromStream{{Stream: "golang"}},
					OCPImageConfigFromStream: OCPImageConfigFromStream{Stream: "golang"},
				},
			},
			streamMap: StreamMap{"golang": {UpstreamImage: "openshift/golang-builder:rhel_8_golang_1.14"}},
			expectedConfig: OCPImageConfig{
				From: OCPImageConfigFrom{
					Builder:                  []OCPImageConfigFromStream{{Stream: "openshift/golang-builder:rhel_8_golang_1.14"}},
					OCPImageConfigFromStream: OCPImageConfigFromStream{Stream: "openshift/golang-builder:rhel_8_golang_1.14"},
				},
			},
		},
		{
			name: "config.from.builder.member gets replaced",
			config: OCPImageConfig{
				From: OCPImageConfigFrom{
					Builder:                  []OCPImageConfigFromStream{{Member: "openshift-enterprise-base"}},
					OCPImageConfigFromStream: OCPImageConfigFromStream{Member: "openshift-enterprise-base"},
				},
				Version: MajorMinor{Major: "4", Minor: "6"},
			},
			allConfigs: map[string]OCPImageConfig{
				"images/openshift-enterprise-base.yml": {
					Name:    "openshift/ose-base",
					Version: MajorMinor{Major: "4", Minor: "6"},
				},
			},
			expectedConfig: OCPImageConfig{
				From: OCPImageConfigFrom{
					Builder: []OCPImageConfigFromStream{{Stream: "registry.svc.ci.openshift.org/ocp/4.6:base"}},
					OCPImageConfigFromStream: OCPImageConfigFromStream{
						Stream: "registry.svc.ci.openshift.org/ocp/4.6:base"},
				},
			},
		},
		{
			name: "both config.from.builder.stream and config.from.builder.member are empty, error",
			config: OCPImageConfig{
				From: OCPImageConfigFrom{
					Builder: []OCPImageConfigFromStream{{}},
				},
			},
			expectedError: utilerrors.NewAggregate([]error{
				errors.New("failed to find replacement for .from.stream"),
				fmt.Errorf("failed to dereference from.builder.%d", 0),
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.config.Version.Major = "4"
			tc.config.Version.Minor = "6"
			tc.expectedConfig.Version.Major = "4"
			tc.expectedConfig.Version.Minor = "6"
			if tc.config.Content == nil {
				tc.config.Content = &OCPImageConfigContent{}
			}
			if tc.expectedConfig.Content == nil {
				tc.expectedConfig.Content = &OCPImageConfigContent{}
			}
			var actualErrMsg string
			err := dereferenceConfig(&tc.config, tc.allConfigs, tc.streamMap, tc.groupYAML)
			if err != nil {
				actualErrMsg = err.Error()
			}
			var expectedErrMsg string
			if tc.expectedError != nil {
				expectedErrMsg = tc.expectedError.Error()
			}
			if actualErrMsg != expectedErrMsg {
				t.Fatalf("expected error %v, got error %v", tc.expectedError, err)
			}
			if err != nil {
				return
			}
			if diff := cmp.Diff(tc.config, tc.expectedConfig); diff != "" {
				t.Errorf("config differs from expectedConfig: %s", diff)
			}
		})
	}
}
