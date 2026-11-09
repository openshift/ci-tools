package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/github"
)

func TestPushEventHandler(t *testing.T) {
	mainKey := repoKey{org: "org", repo: "repo", source: "main"}
	chainedKey := repoKey{org: "org", repo: "repo", source: "release-5.0"}
	stageKey := repoKey{org: "org", repo: "repo", source: "stage"}
	otherRepoKey := repoKey{org: "org", repo: "other-repo", source: "main"}
	otherOrgKey := repoKey{org: "other-org", repo: "repo", source: "main"}
	state := newDesiredState()
	state.replace(map[repoKey]sets.Set[string]{
		mainKey:      sets.New("release-5.0"),
		chainedKey:   sets.New("release-4.23"),
		stageKey:     sets.New("release-5.0"),
		otherRepoKey: sets.New("release-5.0"),
		otherOrgKey:  sets.New("release-5.0"),
	})
	controller := newController(&fakeRefClient{}, state, 1)
	defer controller.queue.ShutDown()
	handler := &pushEventHandler{state: state, controller: controller}

	tests := []struct {
		name     string
		event    github.PushEvent
		wantKeys []string
	}{
		{name: "source push", event: github.PushEvent{Ref: "refs/heads/main", Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}}, wantKeys: []string{"org/repo@main"}},
		{name: "push to target-only branch enqueues its source key", event: github.PushEvent{Ref: "refs/heads/release-4.23", Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}}, wantKeys: []string{"org/repo@release-5.0"}},
		{name: "push to branch that is source and target enqueues every affected key in the repository", event: github.PushEvent{Ref: "refs/heads/release-5.0", Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}}, wantKeys: []string{"org/repo@main", "org/repo@release-5.0", "org/repo@stage"}},
		{name: "unconfigured branch", event: github.PushEvent{Ref: "refs/heads/other", Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}}},
		{name: "tag ignored", event: github.PushEvent{Ref: "refs/tags/main", Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}}},
		{name: "deleted branch ignored", event: github.PushEvent{Ref: "refs/heads/main", Deleted: true, Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler.handle(nil, tc.event)
			got := sets.New[string]()
			for controller.queue.Len() > 0 {
				item, _ := controller.queue.Get()
				controller.queue.Done(item)
				controller.queue.Forget(item)
				got.Insert(item.String())
			}
			want := sets.New(tc.wantKeys...)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("unexpected queued keys (-want +got):\n%s", diff)
			}
		})
	}
}
