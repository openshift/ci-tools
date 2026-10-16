package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

// pipelineConfigFixture includes both explicit branch lists and implicit all-branch repos.
const pipelineConfigFixture = `# controller configuration
orgs:
- org: openshift
  repos:
  - sippy
  - name: origin
    branches:
    - main
    - release-4.20
    - release-4.22
    mode:
      trigger: auto
  - name: ovn-kubernetes
    mode:
      trigger: manual
- org: openshift-priv
  repos:
  - name: origin
    branches:
    - main
    - release-4.22
    mode:
      trigger: auto
- org: Azure
  repos:
  - name: ARO-HCP
    branches:
    - main
`

// pipelineOutput represents a generated ci-operator branch in these unit tests.
func pipelineOutput(org, repo, branch string) config.DataWithInfo {
	return config.DataWithInfo{Info: config.Info{Metadata: api.Metadata{Org: org, Repo: repo, Branch: branch}}}
}

// pipelineBranches reads a named repo's branch list from a YAML document for assertions.
func pipelineBranches(t *testing.T, raw []byte, orgName, repoName string) []string {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parsing output: %v", err)
	}
	for _, org := range mappingValue(document.Content[0], "orgs").Content {
		if mappingValue(org, "org").Value != orgName {
			continue
		}
		for _, repo := range mappingValue(org, "repos").Content {
			if repo.Kind != yaml.MappingNode || mappingValue(repo, "name").Value != repoName {
				continue
			}
			branches := mappingValue(repo, "branches")
			if branches == nil {
				return nil
			}
			result := make([]string, 0, len(branches.Content))
			for _, branch := range branches.Content {
				result = append(result, branch.Value)
			}
			return result
		}
	}
	return nil
}

func TestAddPipelineBranches(t *testing.T) {
	testCases := []struct {
		name        string
		raw         string
		outputs     []config.DataWithInfo
		wantPublic  []string
		wantPrivate []string
		wantChanged bool
		wantError   string
	}{
		{
			name: "adds new release to public and configured private mirror once",
			outputs: []config.DataWithInfo{
				pipelineOutput("openshift", "origin", "release-4.21"),
				pipelineOutput("openshift", "origin", "release-4.21"),
				pipelineOutput("openshift", "origin", "release-4.23"),
			},
			wantPublic:  []string{"main", "release-4.20", "release-4.21", "release-4.22", "release-4.23"},
			wantPrivate: []string{"main", "release-4.21", "release-4.22", "release-4.23"},
			wantChanged: true,
		},
		{
			name:        "private repo can receive its own generated branch",
			outputs:     []config.DataWithInfo{pipelineOutput("openshift-priv", "origin", "release-4.21")},
			wantPublic:  []string{"main", "release-4.20", "release-4.22"},
			wantPrivate: []string{"main", "release-4.21", "release-4.22"},
			wantChanged: true,
		},
		{
			name:        "already configured branch leaves bytes intact",
			outputs:     []config.DataWithInfo{pipelineOutput("openshift", "origin", "release-4.22")},
			wantPublic:  []string{"main", "release-4.20", "release-4.22"},
			wantPrivate: []string{"main", "release-4.22"},
		},
		{
			name:        "unconfigured repo is not enrolled",
			outputs:     []config.DataWithInfo{pipelineOutput("openshift", "other", "release-4.23")},
			wantPublic:  []string{"main", "release-4.20", "release-4.22"},
			wantPrivate: []string{"main", "release-4.22"},
		},
		{
			name:        "repo without explicit branches is not restricted",
			outputs:     []config.DataWithInfo{pipelineOutput("openshift", "ovn-kubernetes", "release-4.23")},
			wantPublic:  []string{"main", "release-4.20", "release-4.22"},
			wantPrivate: []string{"main", "release-4.22"},
		},
		{
			name:        "dev branch is not added",
			outputs:     []config.DataWithInfo{pipelineOutput("openshift", "origin", "main")},
			wantPublic:  []string{"main", "release-4.20", "release-4.22"},
			wantPrivate: []string{"main", "release-4.22"},
		},
		{
			name:        "empty outputs leave bytes intact",
			wantPublic:  []string{"main", "release-4.20", "release-4.22"},
			wantPrivate: []string{"main", "release-4.22"},
		},
		{
			name:      "invalid yaml fails closed",
			raw:       "orgs: [broken",
			outputs:   []config.DataWithInfo{pipelineOutput("openshift", "origin", "release-4.23")},
			wantError: "parsing yaml",
		},
		{
			name:      "missing orgs fails closed",
			raw:       "other: value\n",
			outputs:   []config.DataWithInfo{pipelineOutput("openshift", "origin", "release-4.23")},
			wantError: "expected orgs to be a sequence",
		},
		{
			name:      "malformed branches fail closed",
			raw:       "orgs:\n- org: openshift\n  repos:\n  - name: origin\n    branches: main\n",
			outputs:   []config.DataWithInfo{pipelineOutput("openshift", "origin", "release-4.23")},
			wantError: "expected branches of openshift/origin to be a sequence",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			raw := testCase.raw
			if raw == "" {
				raw = pipelineConfigFixture
			}
			updated, changed, err := addPipelineBranches([]byte(raw), testCase.outputs)
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("expected error containing %q, got %v", testCase.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("addPipelineBranches: %v", err)
			}
			if changed != testCase.wantChanged {
				t.Errorf("changed: want %t, got %t", testCase.wantChanged, changed)
			}
			if diff := cmp.Diff(testCase.wantPublic, pipelineBranches(t, updated, "openshift", "origin")); diff != "" {
				t.Errorf("public branches (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(testCase.wantPrivate, pipelineBranches(t, updated, "openshift-priv", "origin")); diff != "" {
				t.Errorf("private branches (-want +got):\n%s", diff)
			}
			if !changed && !bytes.Equal(updated, []byte(raw)) {
				t.Error("unchanged config was rewritten")
			}
			if !strings.Contains(string(updated), "trigger: manual") || !strings.Contains(string(updated), "# controller configuration") {
				t.Error("unrelated pipeline settings or comments were lost")
			}
			if changed {
				again, changedAgain, err := addPipelineBranches(updated, testCase.outputs)
				if err != nil || changedAgain || !bytes.Equal(again, updated) {
					t.Errorf("rerun is not idempotent: changed=%t, err=%v", changedAgain, err)
				}
			}
		})
	}
}

func TestUpdatePipelineControllerConfig(t *testing.T) {
	testCases := []struct {
		name        string
		initial     string
		outputs     []config.DataWithInfo
		missing     bool
		wantChanged bool
		wantError   string
	}{
		{name: "writes new branch", initial: pipelineConfigFixture, outputs: []config.DataWithInfo{pipelineOutput("openshift", "origin", "release-4.23")}, wantChanged: true},
		{name: "unchanged branch preserves file", initial: pipelineConfigFixture, outputs: []config.DataWithInfo{pipelineOutput("openshift", "origin", "release-4.22")}},
		{name: "missing file reports read error", outputs: []config.DataWithInfo{pipelineOutput("openshift", "origin", "release-4.23")}, missing: true, wantError: "reading pipeline-controller config"},
		{name: "empty outputs do not need a file", missing: true},
		{name: "invalid file reports parse error", initial: "orgs: [broken", outputs: []config.DataWithInfo{pipelineOutput("openshift", "origin", "release-4.23")}, wantError: "parsing yaml"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if !testCase.missing {
				if err := os.WriteFile(path, []byte(testCase.initial), 0644); err != nil {
					t.Fatalf("writing fixture: %v", err)
				}
			}
			err := updatePipelineControllerConfig(path, testCase.outputs)
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("expected error containing %q, got %v", testCase.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("updatePipelineControllerConfig: %v", err)
			}
			if testCase.missing {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("expected file to remain missing, got %v", err)
				}
				return
			}
			updated, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading updated file: %v", err)
			}
			if testCase.wantChanged {
				if !strings.Contains(string(updated), "release-4.23") {
					t.Error("new branch was not written")
				}
			} else if string(updated) != testCase.initial {
				t.Error("unchanged file was rewritten")
			}
		})
	}
}
