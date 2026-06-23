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
  targets:
  - "5.0"
  - "5.1"
  ignore:
  - Azure/ARO-HCP
release_branches:
- source: "5.0"
  targets:
  - "4.23"
  ignore:
  - ignored-org
  - ignored-org/repo
`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadForwardingConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.DefaultBranch.ConfigsPromotingTo != "5.0" || len(config.DefaultBranch.Targets) != 2 || len(config.ReleaseBranches) != 1 {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestForwardingConfigValidation(t *testing.T) {
	tests := map[string]string{
		"empty":                        `{}`,
		"unknown field":                `unknown: true`,
		"invalid selected release":     "default_branch:\n  configs_promoting_to: invalid\n  targets: [\"5.0\"]",
		"empty targets":                "default_branch:\n  configs_promoting_to: \"5.0\"\n  targets: []",
		"duplicate targets":            "default_branch:\n  configs_promoting_to: \"5.0\"\n  targets: [\"5.1\", \"5.1\"]",
		"invalid ignore":               "default_branch:\n  configs_promoting_to: \"5.0\"\n  targets: [\"5.1\"]\n  ignore: [org/repo/extra]",
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
	ignored := []string{"all-org", "specific-org/repo"}
	if !isIgnored(ignored, "all-org", "anything") || !isIgnored(ignored, "specific-org", "repo") || isIgnored(ignored, "specific-org", "other") {
		t.Fatal("org and org/repo ignore matching is incorrect")
	}
}
