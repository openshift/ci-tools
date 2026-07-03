package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadForwardingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forwarding.yaml")
	raw := []byte(`default_branch:
  configs_promoting_to: "5.0"
  forward:
  - family: release
    targets:
    - "5.0"
    - "5.1"
    ignore:
    - org: Azure
      repo: ARO-HCP
  - family: openshift
    targets:
    - "5.0"
    only:
    - org: openshift
      repo: kubecsr
release_branches:
- source: "5.0"
  forward:
  - family: release
    targets:
    - "4.23"
    ignore:
    - ignored-org
    - ignored-org/repo
  - family: openshift
    targets:
    - "4.23"
    - "5.1"
    ignore:
    - org: ignored-org
      repo: exact-repo
      target: openshift-5.1
`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadForwardingConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.DefaultBranch.ConfigsPromotingTo != "5.0" || len(config.DefaultBranch.Forward) != 2 || len(config.ReleaseBranches) != 1 || len(config.ReleaseBranches[0].Forward) != 2 {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestForwardingConfigValidation(t *testing.T) {
	tests := map[string]string{
		"empty":                        `{}`,
		"unknown field":                `unknown: true`,
		"invalid selected release":     "default_branch:\n  configs_promoting_to: invalid\n  targets: [\"5.0\"]",
		"empty targets":                "default_branch:\n  configs_promoting_to: \"5.0\"\n  targets: []",
		"missing forward family":       "default_branch:\n  configs_promoting_to: \"5.0\"\n  forward:\n  - targets: [\"5.0\"]",
		"invalid forward family":       "default_branch:\n  configs_promoting_to: \"5.0\"\n  forward:\n  - family: product\n    targets: [\"5.0\"]",
		"duplicate forward target":     "default_branch:\n  configs_promoting_to: \"5.0\"\n  forward:\n  - family: release\n    targets: [\"5.0\"]\n  - family: release\n    targets: [\"5.0\"]",
		"forward and legacy targets":   "default_branch:\n  configs_promoting_to: \"5.0\"\n  targets: [\"5.0\"]\n  forward:\n  - family: release\n    targets: [\"5.1\"]",
		"forward and top-level ignore": "default_branch:\n  configs_promoting_to: \"5.0\"\n  ignore:\n  - org: Azure\n    repo: ARO-HCP\n  forward:\n  - family: release\n    targets: [\"5.1\"]",
		"duplicate targets":            "default_branch:\n  configs_promoting_to: \"5.0\"\n  targets: [\"5.1\", \"5.1\"]",
		"invalid ignore":               "default_branch:\n  configs_promoting_to: \"5.0\"\n  targets: [\"5.1\"]\n  ignore: [org/repo/extra]",
		"structured org with slash":    "default_branch:\n  configs_promoting_to: \"5.0\"\n  forward:\n  - family: release\n    targets: [\"5.1\"]\n    ignore:\n    - org: org/repo",
		"structured repo with slash":   "default_branch:\n  configs_promoting_to: \"5.0\"\n  forward:\n  - family: release\n    targets: [\"5.1\"]\n    ignore:\n    - org: org\n      repo: repo/extra",
		"invalid structured ignore":    "default_branch:\n  configs_promoting_to: \"5.0\"\n  forward:\n  - family: release\n    targets: [\"5.1\"]\n    ignore:\n    - repo: repo",
		"invalid structured only":      "default_branch:\n  configs_promoting_to: \"5.0\"\n  forward:\n  - family: release\n    targets: [\"5.1\"]\n    only:\n    - repo: repo",
		"unknown ignore field":         "default_branch:\n  configs_promoting_to: \"5.0\"\n  forward:\n  - family: release\n    targets: [\"5.1\"]\n    ignore:\n    - org: org\n      unknown: true",
		"unknown only field":           "default_branch:\n  configs_promoting_to: \"5.0\"\n  forward:\n  - family: release\n    targets: [\"5.1\"]\n    only:\n    - org: org\n      unknown: true",
		"duplicate source":             "release_branches:\n- source: \"5.0\"\n  targets: [\"4.23\"]\n- source: \"5.0\"\n  targets: [\"5.1\"]",
		"release forwards to itself":   "release_branches:\n- source: \"5.0\"\n  targets: [\"5.0\"]",
		"duplicate ignored repository": "release_branches:\n- source: \"5.0\"\n  targets: [\"5.1\"]\n  ignore: [org/repo, org/repo]",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "forwarding.yaml")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadForwardingConfig(path); err == nil {
				t.Fatal("expected invalid configuration to be rejected")
			}
		})
	}
}

func TestIsIgnored(t *testing.T) {
	ignored := []ignoreEntry{
		{Org: "all-org"},
		{Org: "specific-org", Repo: "repo"},
		{Org: "target-org", Repo: "repo", Target: "openshift-5.1"},
	}
	if !isIgnored(ignored, "all-org", "anything", "main", "release-5.0") || !isIgnored(ignored, "specific-org", "repo", "main", "release-5.0") || isIgnored(ignored, "specific-org", "other", "main", "release-5.0") || !isIgnored(ignored, "target-org", "repo", "openshift-5.0", "openshift-5.1") || isIgnored(ignored, "target-org", "repo", "openshift-5.0", "openshift-4.23") {
		t.Fatal("org and org/repo ignore matching is incorrect")
	}
}

func TestIsIncluded(t *testing.T) {
	only := []ignoreEntry{{Org: "org", Repo: "repo"}}
	if !isIncluded(nil, "any", "repo", "main", "release-5.0") || !isIncluded(only, "org", "repo", "main", "release-5.0") || isIncluded(only, "org", "other", "main", "release-5.0") {
		t.Fatal("only matching is incorrect")
	}
}
