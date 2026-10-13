package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestDependenciesFromBody(t *testing.T) {
	for _, tc := range []struct {
		name, body, err string
		want            []prReference
	}{
		{name: "no declarations", body: "This depends on another change.\n- unrelated"},
		{name: "inline mixed case and CRLF", body: "Description\r\nDePeNdS-On: https://github.com/OpenShift/Example/pull/0123\r\nMore text", want: []prReference{{"openshift", "example", 123}}},
		{name: "list and inline", body: "Depends-On:\n\n - org/a#1\n * org/b#2\n + org/c#3\n\n- unrelated\nDepends-On: org/d#4", want: []prReference{{"org", "a", 1}, {"org", "b", 2}, {"org", "c", 3}, {"org", "d", 4}}},
		{name: "bulleted declarations", body: "- Depends-On: org/a#1\n* depends-on: org/b#2\n+ Depends-On: org/c#3", want: []prReference{{"org", "a", 1}, {"org", "b", 2}, {"org", "c", 3}}},
		{name: "list ends at prose", body: "Depends-On:\n- org/a#1\nNext section\n- unrelated", want: []prReference{{"org", "a", 1}}},
		{name: "consecutive sections", body: "Depends-On:\n- org/a#1\nDepends-On:\n- org/b#2", want: []prReference{{"org", "a", 1}, {"org", "b", 2}}},
		{name: "ignore fenced examples", body: "```text\nDepends-On: org/a#1\n```\nDepends-On: org/b#2", want: []prReference{{"org", "b", 2}}},
		{name: "ignore quotes and inline prose", body: "> Depends-On: org/a#1\nUse Depends-On: org/b#2"},
		{name: "empty section", body: "Depends-On:\n\n", err: "section has no PR references"},
		{name: "empty before prose", body: "Depends-On:\nText\n- org/a#1", err: "section has no PR references"},
		{name: "malformed list entry", body: "Depends-On:\n- org/a#1\n- not-a-pr", err: "invalid Depends-On entry"},
		{name: "multiple refs on one line", body: "Depends-On: org/a#1 org/b#2", err: "invalid Depends-On entry"},
		{name: "invalid number", body: "Depends-On: org/a#0", err: "invalid PR number"},
		{name: "non github url", body: "Depends-On: https://example.com/org/a/pull/1", err: "invalid Depends-On entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dependenciesFromBody(tc.body)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("want error containing %q, got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(prReference{})); diff != "" {
				t.Fatalf("dependencies (-want +got):\n%s", diff)
			}
		})
	}
}

func dependencyPR(repo string, number int, body string) github.PullRequest {
	return github.PullRequest{
		Number: number, Body: body, State: "open",
		Base: github.PullRequestBranch{Repo: github.Repo{Owner: github.User{Login: "org"}, Name: repo}, Ref: "main"},
		Head: github.PullRequestBranch{SHA: fmt.Sprintf("sha-%s-%d", repo, number)},
	}
}

type dependencyGithubClient struct {
	fakeGithubClient
	calls map[string]int
}

func (c *dependencyGithubClient) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	key := fmt.Sprintf("%s/%s#%d", org, repo, number)
	c.calls[key]++
	pr := c.prs[key]
	if pr == nil {
		return nil, errors.New("not found")
	}
	return pr, nil
}

func TestResolvePRs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		explicit []string
		deps     []github.PullRequest
		want     []string
		err      string
	}{
		{name: "origin only", want: []string{"org/a#1"}},
		{name: "recursive diamond with duplicate forms", body: "Depends-On: org/b#2\nDepends-On: org/c#3\nDepends-On: https://github.com/ORG/B/pull/2", deps: []github.PullRequest{
			dependencyPR("b", 2, "Depends-On: org/d#4"), dependencyPR("c", 3, "Depends-On: org/d#4"), dependencyPR("d", 4, ""),
		}, want: []string{"org/d#4", "org/b#2", "org/c#3", "org/a#1"}},
		{name: "explicit also has dependencies", explicit: []string{"org/b#2", "https://github.com/org/b/pull/2"}, body: "Depends-On: org/b#2", deps: []github.PullRequest{
			dependencyPR("b", 2, "Depends-On: org/c#3"), dependencyPR("c", 3, ""),
		}, want: []string{"org/c#3", "org/b#2", "org/a#1"}},
		{name: "explicit depends on origin", explicit: []string{"org/b#2"}, deps: []github.PullRequest{dependencyPR("b", 2, "Depends-On: org/a#1")}, want: []string{"org/a#1", "org/b#2"}},
		{name: "explicit origin is deduplicated", explicit: []string{"org/a#1"}, want: []string{"org/a#1"}},
		{name: "self cycle", body: "Depends-On: org/a#1", err: "org/a#1 -> org/a#1"},
		{name: "indirect cycle", body: "Depends-On: org/b#2", deps: []github.PullRequest{dependencyPR("b", 2, "Depends-On: org/c#3"), dependencyPR("c", 3, "Depends-On: org/a#1")}, err: "org/a#1 -> org/b#2 -> org/c#3 -> org/a#1"},
		{name: "merged dependency stops traversal", body: "Depends-On: org/b#2", deps: []github.PullRequest{{Number: 2, Merged: true, State: "closed", Body: "Depends-On: org/missing#3"}}, want: []string{"org/a#1"}},
		{name: "closed dependency", body: "Depends-On: org/b#2", deps: []github.PullRequest{{Number: 2, State: "closed"}}, err: "org/b#2 is closed without merging"},
		{name: "explicit closed PR preserved", explicit: []string{"org/b#2"}, body: "Depends-On: org/b#2", deps: []github.PullRequest{{Number: 2, State: "closed"}}, want: []string{"org/b#2", "org/a#1"}},
		{name: "explicit merged PR preserved", explicit: []string{"org/b#2"}, deps: []github.PullRequest{{Number: 2, State: "closed", Merged: true}}, want: []string{"org/b#2", "org/a#1"}},
		{name: "missing dependency", body: "Depends-On: org/b#2", err: "couldn't get PR from GitHub: org/b#2: not found"},
		{name: "malformed nested declaration identifies source", body: "Depends-On: org/b#2", deps: []github.PullRequest{dependencyPR("b", 2, "Depends-On: wrong")}, err: "PR org/b#2: invalid Depends-On entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &dependencyGithubClient{fakeGithubClient: fakeGithubClient{prs: map[string]*github.PullRequest{}}, calls: map[string]int{}}
			for _, pr := range tc.deps {
				if pr.Base.Repo.Name == "" {
					pr.Base = dependencyPR("b", 2, "").Base
				}
				client.prs[referenceForPR(pr).String()] = &pr
			}
			s := server{ghc: client}
			got, err := s.resolvePRs(dependencyPR("a", 1, tc.body), tc.explicit, map[prReference]github.PullRequest{})
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("want error containing %q, got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var refs []string
			for _, pr := range got {
				refs = append(refs, referenceForPR(pr).String())
			}
			if diff := cmp.Diff(tc.want, refs); diff != "" {
				t.Fatalf("ordered PRs (-want +got):\n%s", diff)
			}
			for ref, calls := range client.calls {
				if calls != 1 {
					t.Errorf("fetched %s %d times", ref, calls)
				}
			}
		})
	}
}

func TestDependencyLimit(t *testing.T) {
	for _, count := range []int{maxPRs, maxPRs + 1} {
		for _, chain := range []bool{true, false} {
			t.Run(fmt.Sprintf("count=%d/chain=%t", count, chain), func(t *testing.T) {
				client := &dependencyGithubClient{fakeGithubClient: fakeGithubClient{prs: map[string]*github.PullRequest{}}, calls: map[string]int{}}
				origin := dependencyPR("a", 1, "")
				for i := 2; i <= count; i++ {
					pr := dependencyPR("a", i, "")
					if chain && i < count {
						pr.Body = fmt.Sprintf("Depends-On: org/a#%d", i+1)
					}
					if !chain {
						pr.Merged = true // Satisfied dependencies must also bound API calls.
						origin.Body += fmt.Sprintf("Depends-On: org/a#%d\n", i)
					}
					client.prs[referenceForPR(pr).String()] = &pr
				}
				if chain {
					origin.Body = "Depends-On: org/a#2"
				}
				s := server{ghc: client}
				_, err := s.resolvePRs(origin, nil, map[prReference]github.PullRequest{})
				if count == maxPRs && err != nil {
					t.Fatal(err)
				}
				if count > maxPRs && (err == nil || !strings.Contains(err.Error(), "exceeds the max")) {
					t.Fatalf("expected graph limit error, got %v", err)
				}
				if len(client.calls) != maxPRs-1 {
					t.Fatalf("expected %d API calls, got %d", maxPRs-1, len(client.calls))
				}
			})
		}
	}
}

func TestDetermineDependencyJobRuns(t *testing.T) {
	dep := dependencyPR("a", 2, "")
	origin := dependencyPR("a", 1, "Depends-On:\n- org/a#2")
	client := &dependencyGithubClient{fakeGithubClient: fakeGithubClient{prs: map[string]*github.PullRequest{"org/a#2": &dep}}, calls: map[string]int{}}
	s := server{ghc: client}
	jobs, err := s.determineJobRuns("/testwith org/a/main/unit\r\n/testwith org/a/dev/unit org/a#2\n/testwith org/a/main/unit", origin)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
	for i, branch := range []string{"main", "dev", "main"} {
		if len(jobs[i].AdditionalPRs) != 1 || jobs[i].AdditionalPRs[0].Base.Ref != branch {
			t.Errorf("job %d has unexpected additional PRs: %+v", i, jobs[i].AdditionalPRs)
		}
		if len(jobs[i].ResolvedPRs) != 2 || jobs[i].ResolvedPRs[0].Number != 2 || jobs[i].ResolvedPRs[1].Number != 1 {
			t.Errorf("job %d has wrong dependency order: %+v", i, jobs[i].ResolvedPRs)
		}
	}
	if dep.Base.Ref != "main" || client.calls["org/a#2"] != 1 {
		t.Fatalf("cached PR was mutated or fetched repeatedly: %+v, calls=%v", dep, client.calls)
	}
}

func TestGenerateDependencyProwJob(t *testing.T) {
	for _, tc := range []struct {
		name, body, explicit   string
		deps                   []github.PullRequest
		wantPrimary, wantExtra []int
	}{
		{name: "origin alone", wantPrimary: []int{1}},
		{name: "description dependencies across repositories", body: "Depends-On: org/a#2\nDepends-On: org/b#4", deps: []github.PullRequest{
			dependencyPR("a", 2, "Depends-On: org/a#3"), dependencyPR("a", 3, ""), dependencyPR("b", 4, ""),
		}, wantPrimary: []int{3, 2, 1}, wantExtra: []int{4}},
		{name: "explicit PR depends on origin", explicit: " org/a#2", deps: []github.PullRequest{dependencyPR("a", 2, "Depends-On: org/a#1")}, wantPrimary: []int{1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := api.Metadata{Org: "org", Repo: "a", Branch: "dev"}
			configs := map[api.Metadata]*api.ReleaseBuildConfiguration{
				metadata:                                {Tests: []api.TestStepConfiguration{{As: "unit"}}},
				{Org: "org", Repo: "b", Branch: "main"}: {},
			}
			client := &dependencyGithubClient{fakeGithubClient: fakeGithubClient{prs: map[string]*github.PullRequest{}}, calls: map[string]int{}}
			for _, pr := range tc.deps {
				client.prs[referenceForPR(pr).String()] = &pr
			}
			s := server{
				ghc:                client,
				ciOpConfigResolver: &fakeCIOpConfigResolver{configs: configs},
				prowConfigGetter:   &fakeProwConfigGetter{cfg: &config.Config{}},
				dispatcherClient:   &fakeDispatcherClient{},
				namespace:          "ci",
			}
			jobs, err := s.determineJobRuns("/testwith org/a/dev/unit"+tc.explicit, dependencyPR("a", 1, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if len(jobs) != 1 {
				t.Fatalf("expected one job, got %d", len(jobs))
			}
			pj, err := s.generateProwJob(jobs[0])
			if err != nil {
				t.Fatal(err)
			}
			var primary, extra []int
			for _, pull := range pj.Spec.Refs.Pulls {
				primary = append(primary, pull.Number)
				if pull.SHA != fmt.Sprintf("sha-a-%d", pull.Number) {
					t.Errorf("incorrect SHA for primary pull: %+v", pull)
				}
			}
			for _, ref := range pj.Spec.ExtraRefs {
				if ref.Org != "org" || ref.Repo != "b" || ref.BaseRef != "main" {
					t.Errorf("unexpected extra ref: %+v", ref)
				}
				for _, pull := range ref.Pulls {
					extra = append(extra, pull.Number)
				}
			}
			if pj.Spec.Refs.BaseRef != "dev" {
				t.Errorf("primary branch = %q, want dev", pj.Spec.Refs.BaseRef)
			}
			if diff := cmp.Diff(tc.wantPrimary, primary); diff != "" {
				t.Errorf("primary pull order (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantExtra, extra); diff != "" {
				t.Errorf("extra pulls (-want +got):\n%s", diff)
			}
		})
	}
}
