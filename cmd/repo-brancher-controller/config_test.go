package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

func writeConfig(t *testing.T, root, org, repo, branch string) {
	writeConfigWithPromotion(t, root, org, repo, branch, false)
}

func writeConfigWithPromotion(t *testing.T, root, org, repo, branch string, disabled bool) {
	t.Helper()
	dir := filepath.Join(root, org, repo)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	disabledLine := ""
	if disabled {
		disabledLine = "    disabled: true\n"
	}
	raw := []byte(fmt.Sprintf(`promotion:
  to:
  - name: %q
%s
    namespace: ocp
resources:
  '*':
    requests:
      cpu: 10m
tests:
- as: unit
  commands: "true"
  container:
    from: src
zz_generated_metadata:
  branch: %s
  org: %s
  repo: %s
`, "5.0", disabledLine, branch, org, repo))
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-%s.yaml", org, repo, branch))
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDesiredStateUsesBranchCategoriesAndScopedIgnores(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, configDir, "org", "default", "main")
	writeConfig(t, configDir, "org", "release", "release-5.0")
	writeConfig(t, configDir, "org", "openshift-release", "openshift-5.0")
	writeConfig(t, configDir, "org", "wrong-release", "release-4.23")
	writeConfig(t, configDir, "ignored-default", "repo", "master")
	writeConfig(t, configDir, "org", "ignored-release", "release-5.0")
	writeConfigWithPromotion(t, configDir, "org", "disabled-default", "main", true)
	writeConfigWithPromotion(t, configDir, "org", "disabled-release", "release-5.0", true)

	rules := &forwardingConfig{
		DefaultBranch: &defaultBranchForwarding{
			ConfigsPromotingTo: "5.0",
			Forward: []forwardBlock{{
				Family:  branchFamilyRelease,
				Targets: []string{"5.0", "5.1"},
				Ignore:  []ignoreEntry{{Org: "ignored-default"}},
			}},
		},
		ReleaseBranches: []releaseBranchForwarding{{
			Source: "5.0",
			Forward: []forwardBlock{{
				Family:  branchFamilyRelease,
				Targets: []string{"4.23"},
				Ignore:  []ignoreEntry{{Org: "org", Repo: "ignored-release"}},
			}, {
				Family:  branchFamilyOpenShift,
				Targets: []string{"4.23"},
			}},
		}},
	}
	state, err := loadDesiredState(configDir, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := map[repoKey]sets.Set[string]{
		{org: "org", repo: "default", source: "main"}:                    sets.New("release-5.0", "release-5.1"),
		{org: "org", repo: "release", source: "release-5.0"}:             sets.New("release-4.23"),
		{org: "org", repo: "openshift-release", source: "openshift-5.0"}: sets.New("openshift-4.23"),
		{org: "org", repo: "disabled-release", source: "release-5.0"}:    sets.New("release-4.23"),
	}
	if len(state) != len(want) {
		t.Fatalf("unexpected desired state: want %v, got %v", want, state)
	}
	for key, targets := range want {
		if got, ok := state[key]; !ok || !got.Equal(targets) {
			t.Fatalf("unexpected targets for %s: want %v, got %v", key, targets, got)
		}
	}
}

func TestLoadDesiredStateSupportsDefaultBranchOpenShiftFamily(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, configDir, "org", "default", "main")
	writeConfig(t, configDir, "org", "ignored-openshift", "main")
	writeConfig(t, configDir, "org", "excluded-openshift", "main")

	rules := &forwardingConfig{
		DefaultBranch: &defaultBranchForwarding{
			ConfigsPromotingTo: "5.0",
			Forward: []forwardBlock{{
				Family:  branchFamilyRelease,
				Targets: []string{"5.0"},
			}, {
				Family:  branchFamilyOpenShift,
				Targets: []string{"5.0", "5.1"},
				Only: []ignoreEntry{{
					Org: "org", Repo: "default",
				}, {
					Org: "org", Repo: "ignored-openshift",
				}},
				Ignore: []ignoreEntry{{
					Org: "org", Repo: "ignored-openshift",
				}},
			}},
		},
	}
	state, err := loadDesiredState(configDir, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := map[repoKey]sets.Set[string]{
		{org: "org", repo: "default", source: "main"}:            sets.New("release-5.0", "openshift-5.0", "openshift-5.1"),
		{org: "org", repo: "ignored-openshift", source: "main"}:  sets.New("release-5.0"),
		{org: "org", repo: "excluded-openshift", source: "main"}: sets.New("release-5.0"),
	}
	if len(state) != len(want) {
		t.Fatalf("unexpected desired state: want %v, got %v", want, state)
	}
	for key, targets := range want {
		if got, ok := state[key]; !ok || !got.Equal(targets) {
			t.Fatalf("unexpected targets for %s: want %v, got %v", key, targets, got)
		}
	}
}

func TestLoadDesiredStateLegacyConfigCompatibility(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, configDir, "org", "default", "main")
	writeConfig(t, configDir, "org", "release", "release-5.0")
	writeConfig(t, configDir, "org", "openshift-release", "openshift-5.0")

	rules := &forwardingConfig{
		DefaultBranch: &defaultBranchForwarding{
			ConfigsPromotingTo: "5.0",
			Targets:            []string{"5.1"},
		},
		ReleaseBranches: []releaseBranchForwarding{{
			Source:  "5.0",
			Targets: []string{"4.23"},
		}},
	}
	state, err := loadDesiredState(configDir, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := map[repoKey]sets.Set[string]{
		{org: "org", repo: "default", source: "main"}:                    sets.New("release-5.1"),
		{org: "org", repo: "release", source: "release-5.0"}:             sets.New("release-4.23"),
		{org: "org", repo: "openshift-release", source: "openshift-5.0"}: sets.New("openshift-4.23"),
	}
	if len(state) != len(want) {
		t.Fatalf("unexpected desired state: want %v, got %v", want, state)
	}
	for key, targets := range want {
		if got, ok := state[key]; !ok || !got.Equal(targets) {
			t.Fatalf("unexpected targets for %s: want %v, got %v", key, targets, got)
		}
	}
}
