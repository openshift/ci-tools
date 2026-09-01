package main

import (
	"testing"

	"sigs.k8s.io/prow/pkg/github"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

func TestPushTouchesCIOperator(t *testing.T) {
	testCases := []struct {
		name   string
		event  github.PushEvent
		expect bool
	}{
		{
			name: "added file",
			event: github.PushEvent{
				Commits: []github.Commit{{Added: []string{".ci-operator/ci-operator.yaml"}}},
			},
			expect: true,
		},
		{
			name: "modified file",
			event: github.PushEvent{
				Commits: []github.Commit{{Modified: []string{".ci-operator/ci-operator.yaml"}}},
			},
			expect: true,
		},
		{
			name: "removed file",
			event: github.PushEvent{
				Commits: []github.Commit{{Removed: []string{".ci-operator/ci-operator.yaml"}}},
			},
			expect: true,
		},
		{
			name: "single-file config",
			event: github.PushEvent{
				Commits: []github.Commit{{Added: []string{".ci-operator.yaml"}}},
			},
			expect: true,
		},
		{
			name: "unrelated file",
			event: github.PushEvent{
				Commits: []github.Commit{{Modified: []string{"main.go"}}},
			},
			expect: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pushTouchesCIOperator(tc.event); got != tc.expect {
				t.Errorf("expected %v, got %v", tc.expect, got)
			}
		})
	}
}

func TestMetadataFromFilename(t *testing.T) {
	testCases := []struct {
		filename string
		expected *cioperatorapi.Metadata
	}{
		{
			filename: "ci-operator.yaml",
			expected: &cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "main"},
		},
		{
			filename: "ci-operator__aws.yaml",
			expected: &cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "main", Variant: "aws"},
		},
		{
			filename: "ci-operator__multi-arch.yml",
			expected: &cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "main", Variant: "multi-arch"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			result := metadataFromFilename(tc.filename, "org", "repo", "main")
			if result.Org != tc.expected.Org || result.Repo != tc.expected.Repo ||
				result.Branch != tc.expected.Branch || result.Variant != tc.expected.Variant {
				t.Errorf("expected %+v, got %+v", tc.expected, result)
			}
		})
	}
}
