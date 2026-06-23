package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"k8s.io/apimachinery/pkg/util/sets"
)

type fakeRefClient struct {
	refs    map[string]string
	creates []string
	updates []string
	err     error
}

func refID(org, repo, branch string) string { return fmt.Sprintf("%s/%s@%s", org, repo, branch) }

func (f *fakeRefClient) getRef(_ context.Context, org, repo, branch string) (string, bool, error) {
	sha, ok := f.refs[refID(org, repo, branch)]
	return sha, ok, nil
}

func (f *fakeRefClient) createRef(_ context.Context, org, repo, branch, sha string) error {
	f.creates = append(f.creates, refID(org, repo, branch)+"="+sha)
	if f.err == nil {
		f.refs[refID(org, repo, branch)] = sha
	}
	return f.err
}

func (f *fakeRefClient) updateRef(_ context.Context, org, repo, branch, sha string) error {
	f.updates = append(f.updates, refID(org, repo, branch)+"="+sha)
	if f.err == nil {
		f.refs[refID(org, repo, branch)] = sha
	}
	return f.err
}

func counterValue(t *testing.T, label string) float64 {
	t.Helper()
	metric := &dto.Metric{}
	if err := reconciliationTotal.WithLabelValues(label).Write(metric); err != nil {
		t.Fatal(err)
	}
	return metric.GetCounter().GetValue()
}

func TestReconcile(t *testing.T) {
	key := repoKey{org: "org", repo: "repo", source: "main"}
	state := newDesiredState()
	state.replace(map[repoKey]sets.Set[string]{key: sets.New("release-4.20", "release-4.21", "release-4.22")})
	refs := &fakeRefClient{refs: map[string]string{
		refID("org", "repo", "main"):         "new",
		refID("org", "repo", "release-4.20"): "new",
		refID("org", "repo", "release-4.21"): "old",
	}}
	c := newController(refs, state, 1)
	defer c.queue.ShutDown()
	if err := c.reconcile(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if len(refs.updates) != 1 || refs.updates[0] != "org/repo@release-4.21=new" {
		t.Fatalf("unexpected updates: %v", refs.updates)
	}
	if len(refs.creates) != 1 || refs.creates[0] != "org/repo@release-4.22=new" {
		t.Fatalf("unexpected creates: %v", refs.creates)
	}
}

func TestDesiredStateDeduplicatesTargets(t *testing.T) {
	key := repoKey{org: "org", repo: "repo", source: "main"}
	s := newDesiredState()
	changed := s.replace(map[repoKey]sets.Set[string]{key: sets.New("release-4.20", "release-4.20")})
	if len(changed) != 1 {
		t.Fatalf("expected one changed repository, got %v", changed)
	}
	changed = s.replace(map[repoKey]sets.Set[string]{key: sets.New("release-4.20")})
	if len(changed) != 0 {
		t.Fatalf("equivalent desired state was reported changed: %v", changed)
	}
	if got := s.matching("org", "repo", "main"); len(got) != 1 || got[0] != key {
		t.Fatalf("unexpected webhook match: %v", got)
	}
}

type targetErrorRefClient struct{ fakeRefClient }

func (f *targetErrorRefClient) updateRef(_ context.Context, _, _, branch, _ string) error {
	if branch == "release-5.0" {
		return permanentError{errors.New("not a fast forward")}
	}
	return errors.New("temporary network failure")
}

func TestReconcilePreservesRetryableSiblingError(t *testing.T) {
	key := repoKey{org: "org", repo: "repo", source: "main"}
	state := newDesiredState()
	state.replace(map[repoKey]sets.Set[string]{key: sets.New("release-5.0", "release-5.1")})
	refs := &targetErrorRefClient{fakeRefClient: fakeRefClient{refs: map[string]string{
		refID("org", "repo", "main"):        "new",
		refID("org", "repo", "release-5.0"): "old",
		refID("org", "repo", "release-5.1"): "old",
	}}}
	c := newController(refs, state, 1)
	defer c.queue.ShutDown()
	err := c.reconcile(context.Background(), key)
	var aggregate *reconciliationErrors
	if !errors.As(err, &aggregate) || len(aggregate.permanent) != 1 || len(aggregate.retryable) != 1 {
		t.Fatalf("expected one permanent and one retryable error, got %#v", err)
	}
}

func TestProcessNextReportsPermanentErrorWithRetryableSibling(t *testing.T) {
	key := repoKey{org: "org", repo: "repo", source: "main"}
	state := newDesiredState()
	state.replace(map[repoKey]sets.Set[string]{key: sets.New("release-5.0", "release-5.1")})
	refs := &targetErrorRefClient{fakeRefClient: fakeRefClient{refs: map[string]string{
		refID("org", "repo", "main"):        "new",
		refID("org", "repo", "release-5.0"): "old",
		refID("org", "repo", "release-5.1"): "old",
	}}}
	c := newController(refs, state, 1)
	defer c.queue.ShutDown()

	beforePermanent := counterValue(t, "permanent_error")
	beforeRetry := counterValue(t, "retry")
	c.enqueue(key)
	if !c.processNext(context.Background()) {
		t.Fatal("processNext unexpectedly stopped")
	}
	if got := counterValue(t, "permanent_error"); got != beforePermanent+1 {
		t.Fatalf("permanent error metric: want %v, got %v", beforePermanent+1, got)
	}
	if got := counterValue(t, "retry"); got != beforeRetry+1 {
		t.Fatalf("retry metric: want %v, got %v", beforeRetry+1, got)
	}
}

func TestRemovedTargetIsNotMutated(t *testing.T) {
	key := repoKey{org: "org", repo: "repo", source: "main"}
	state := newDesiredState()
	state.replace(map[repoKey]sets.Set[string]{key: sets.New("release-5.0")})
	state.replace(map[repoKey]sets.Set[string]{})
	called, err := state.ifTargetConfigured(key, "release-5.0", func() error {
		t.Fatal("mutation called for removed target")
		return nil
	})
	if err != nil || called {
		t.Fatalf("unexpected result: called=%v err=%v", called, err)
	}
}

func TestReplacementWaitsForRepositoryMutation(t *testing.T) {
	key := repoKey{org: "org", repo: "repo", source: "main"}
	state := newDesiredState()
	state.replace(map[repoKey]sets.Set[string]{key: sets.New("release-5.0")})
	mutationStarted, releaseMutation := make(chan struct{}), make(chan struct{})
	mutationDone := make(chan struct{})
	go func() {
		defer close(mutationDone)
		_, _ = state.ifTargetConfigured(key, "release-5.0", func() error {
			close(mutationStarted)
			<-releaseMutation
			return nil
		})
	}()
	<-mutationStarted
	replacementDone := make(chan struct{})
	go func() {
		state.replace(map[repoKey]sets.Set[string]{})
		close(replacementDone)
	}()
	select {
	case <-replacementDone:
		t.Fatal("replacement completed while mutation was active")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseMutation)
	<-mutationDone
	select {
	case <-replacementDone:
	case <-time.After(time.Second):
		t.Fatal("replacement did not complete after mutation")
	}
}
