package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestValidateExternalPluginRegistrations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "_plugins.yaml"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	write := func(org, config string) {
		t.Helper()
		dir := filepath.Join(root, org)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "_pluginconfig.yaml"), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("registered", `external_plugins:
  registered:
  - endpoint: http://repo-brancher-controller
    events: [push]
    name: repo-brancher-controller
`)
	write("default-endpoint", `external_plugins:
  default-endpoint:
  - events: [push]
    name: repo-brancher-controller
`)
	write("trailing-slash", `external_plugins:
  trailing-slash:
  - endpoint: http://repo-brancher-controller/
    events: [push]
    name: repo-brancher-controller
`)
	write("missing", `plugins: {}`)
	write("repository", `external_plugins:
  repository/repo:
  - events: [push]
    name: repo-brancher-controller
`)
	desired := map[repoKey]sets.Set[string]{
		{org: "registered", repo: "repo", source: "main"}:       sets.New("release-5.0"),
		{org: "default-endpoint", repo: "repo", source: "main"}: sets.New("release-5.0"),
		{org: "trailing-slash", repo: "repo", source: "main"}:   sets.New("release-5.0"),
		{org: "repository", repo: "repo", source: "main"}:       sets.New("release-5.0"),
	}
	if err := validateExternalPluginRegistrations(root, desired); err != nil {
		t.Fatalf("valid registration rejected: %v", err)
	}
	desired[repoKey{org: "missing", repo: "repo", source: "main"}] = sets.New("release-5.0")
	err := validateExternalPluginRegistrations(root, desired)
	if err == nil {
		t.Fatal("missing registration was accepted")
	}
	var missing missingExternalPluginRegistrationError
	if !errors.As(err, &missing) {
		t.Fatalf("expected missing registration error, got %T: %v", err, err)
	}
	if !shouldContinueAfterPluginRegistrationError(err) {
		t.Fatal("missing registration should not block desired-state reload")
	}
	if shouldContinueAfterPluginRegistrationError(errors.New("load plugin config")) {
		t.Fatal("non-registration errors should still block desired-state reload")
	}
}
