package main

import (
	"testing"

	"sigs.k8s.io/yaml"
)

func TestOCPImageConfig(t *testing.T) {
	in := []byte(`content:
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
- multus-dev@redhat.com`)

	ocpImageConfig := &ocpImageConfig{}
	if err := yaml.Unmarshal(in, ocpImageConfig); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if ocpImageConfig.Content.Source.Git.URL != "git@github.com:openshift/whereabouts-cni.git" {
		t.Errorf("expected ocpImageConfig.Content.Source.Git.URL to be 'git@github.com:openshift/whereabouts-cni.git', was %q", ocpImageConfig.Content.Source.Git.URL)
	}

	if ocpImageConfig.From.Stream != "rhel" {
		t.Errorf("expected 'ocpImageConfig.From.Stream' to be 'rhel', was %q", ocpImageConfig.From.Stream)
	}
	if ocpImageConfig.Name != "openshift/ose-multus-whereabouts-ipam-cni" {
		t.Errorf("expected .Name to be 'openshift/ose-multus-whereabouts-ipam-cni', was %q", ocpImageConfig.Name)
	}
}
