package main

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/config/org"
	"k8s.io/test-infra/prow/github/fakegithub"
)

func TestGenerateRepositories(t *testing.T) {
	pntrBool := func(b bool) *bool { return &b }
	pntrString := func(s string) *string { return &s }

	orgRepos := map[string]sets.Set[string]{
		"openshift": sets.New[string]([]string{"repo1", "repo2"}...),
		"testshift": sets.New[string]([]string{"repo3", "repo4"}...),
	}

	expectedRepos := map[string]org.Repo{
		"repo1": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo1"),
			Private:          pntrBool(true),
		},
		"repo2": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo2"),
			Private:          pntrBool(true),
		},
		"repo3": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo3"),
			Private:          pntrBool(true),
		},
		"repo4": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo4"),
			Private:          pntrBool(true),
		},
	}

	repos := generateRepositories(&fakegithub.FakeClient{}, orgRepos, logrus.WithField("destination-org", "testOrg"))
	if !reflect.DeepEqual(repos, expectedRepos) {
		t.Fatal(cmp.Diff(repos, expectedRepos))
	}
}
