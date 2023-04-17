package prowconfigutils_test

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/prowconfigutils"
)

func TestExtractOrgRepoBranch(t *testing.T) {
	for _, testcase := range []struct {
		name          string
		orgRepoBranch string
		wantOrg       string
		wantRepo      string
		wantBranch    string
	}{
		{
			name:          "org/repo@branch shorthand",
			orgRepoBranch: "openshift/ci-tools@master",
			wantOrg:       "openshift",
			wantRepo:      "ci-tools",
			wantBranch:    "master",
		},
		{
			name:          "org/repo shorthand",
			orgRepoBranch: "openshift/ci-tools",
			wantOrg:       "openshift",
			wantRepo:      "ci-tools",
			wantBranch:    "",
		},
		{
			name:          "org shorthand",
			orgRepoBranch: "openshift",
			wantOrg:       "openshift",
			wantRepo:      "",
			wantBranch:    "",
		},
		{
			name:          "nothing",
			orgRepoBranch: "",
			wantOrg:       "",
			wantRepo:      "",
			wantBranch:    "",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			org, repo, branch := prowconfigutils.ExtractOrgRepoBranch(testcase.orgRepoBranch)
			if org != testcase.wantOrg {
				t.Errorf("orgs mismatch: got %q want %q", org, testcase.wantOrg)
			}
			if repo != testcase.wantRepo {
				t.Errorf("repos mismatch: got %q want %q", repo, testcase.wantRepo)
			}
			if branch != testcase.wantBranch {
				t.Errorf("branches mismatch: got %q want %q", branch, testcase.wantBranch)
			}
		})
	}
}
