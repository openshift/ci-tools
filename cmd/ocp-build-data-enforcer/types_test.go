package main

import (
	"testing"

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
			ocpImageConfig := &ocpImageConfig{}
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
