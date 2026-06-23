package main

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/github"
)

func TestPushEventHandler(t *testing.T) {
	key := repoKey{org: "org", repo: "repo", source: "main"}
	state := newDesiredState()
	state.replace(map[repoKey]sets.Set[string]{key: sets.New("release-5.0")})
	controller := newController(&fakeRefClient{}, state, 1)
	defer controller.queue.ShutDown()
	handler := &pushEventHandler{state: state, controller: controller}

	tests := []struct {
		name      string
		event     github.PushEvent
		wantQueue int
	}{
		{name: "source push", event: github.PushEvent{Ref: "refs/heads/main", Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}}, wantQueue: 1},
		{name: "tag ignored", event: github.PushEvent{Ref: "refs/tags/main", Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}}},
		{name: "deleted branch ignored", event: github.PushEvent{Ref: "refs/heads/main", Deleted: true, Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for controller.queue.Len() > 0 {
				item, _ := controller.queue.Get()
				controller.queue.Done(item)
				controller.queue.Forget(item)
			}
			handler.handle(nil, tc.event)
			if controller.queue.Len() != tc.wantQueue {
				t.Fatalf("queue length: want %d, got %d", tc.wantQueue, controller.queue.Len())
			}
		})
	}
}
