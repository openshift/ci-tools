package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

func pushTouchesCIOperator(pe github.PushEvent) bool {
	for _, commit := range pe.Commits {
		for _, files := range [][]string{commit.Added, commit.Modified, commit.Removed} {
			for _, f := range files {
				if strings.HasPrefix(f, ciOperatorDir+"/") || f == ".ci-operator.yaml" {
					return true
				}
			}
		}
	}
	return false
}

func metadataFromFilename(filename, org, repo, branch string) *cioperatorapi.Metadata {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	var variant string
	if _, after, found := strings.Cut(base, "__"); found {
		variant = after
	}
	return &cioperatorapi.Metadata{
		Org:     org,
		Repo:    repo,
		Branch:  branch,
		Variant: variant,
	}
}

func periodicTestName(jobName, org, repo, branch string) string {
	prefix := fmt.Sprintf("periodic-ci-%s-%s-%s-", org, repo, branch)
	if name, ok := strings.CutPrefix(jobName, prefix); ok {
		return name
	}
	return jobName
}

func filterSelfRef(refs []pjapi.Refs, org, repo string) []pjapi.Refs {
	var filtered []pjapi.Refs
	for _, ref := range refs {
		if ref.Org == org && ref.Repo == repo {
			continue
		}
		filtered = append(filtered, ref)
	}
	return filtered
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
