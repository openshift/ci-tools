package main

import (
	"context"
	"errors"
	"testing"

	"sigs.k8s.io/prow/pkg/github"
)

type fakeProwRefClient struct {
	refs      map[string]string
	getErr    error
	createErr error
	updateErr error
	creates   []string
	updates   []string
}

func (f *fakeProwRefClient) GetRefWithContext(_ context.Context, org, repo, ref string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	sha, ok := f.refs[org+"/"+repo+"@"+ref]
	if !ok {
		return "", github.NewNotFound()
	}
	return sha, nil
}

func (f *fakeProwRefClient) CreateRefWithContext(_ context.Context, org, repo, ref, sha string) error {
	f.creates = append(f.creates, org+"/"+repo+"@"+ref+"="+sha)
	return f.createErr
}

func (f *fakeProwRefClient) UpdateRefWithContext(_ context.Context, org, repo, ref, sha string, force bool) error {
	f.updates = append(f.updates, org+"/"+repo+"@"+ref+"="+sha)
	if force {
		return errors.New("force must remain false")
	}
	return f.updateErr
}

func TestGitHubRefClientUsesProwRefOperations(t *testing.T) {
	upstream := &fakeProwRefClient{refs: map[string]string{"org/repo@heads/main": "source-sha"}}
	client := newGitHubRefClient(upstream, nil)
	sha, exists, err := client.getRef(context.Background(), "org", "repo", "main")
	if err != nil || !exists || sha != "source-sha" {
		t.Fatalf("get source: sha=%q exists=%v err=%v", sha, exists, err)
	}
	if _, exists, err := client.getRef(context.Background(), "org", "repo", "target"); err != nil || exists {
		t.Fatalf("get missing target: exists=%v err=%v", exists, err)
	}
	if err := client.createRef(context.Background(), "org", "repo", "target", sha); err != nil {
		t.Fatal(err)
	}
	if err := client.updateRef(context.Background(), "org", "repo", "target", sha); err != nil {
		t.Fatal(err)
	}
	if len(upstream.creates) != 1 || upstream.creates[0] != "org/repo@refs/heads/target=source-sha" {
		t.Fatalf("unexpected creates: %v", upstream.creates)
	}
	if len(upstream.updates) != 1 || upstream.updates[0] != "org/repo@heads/target=source-sha" {
		t.Fatalf("unexpected updates: %v", upstream.updates)
	}
}

func TestUpdateRefValidationFailureIsPermanent(t *testing.T) {
	upstream := &fakeProwRefClient{updateErr: github.NewUnprocessableEntity()}
	err := newGitHubRefClient(upstream, nil).updateRef(context.Background(), "org", "repo", "target", "sha")
	var permanent permanentError
	if !errors.As(err, &permanent) {
		t.Fatalf("expected permanent error, got %T: %v", err, err)
	}
}

func TestCreateRefRecoversFromConcurrentCreation(t *testing.T) {
	upstream := &fakeProwRefClient{
		refs:      map[string]string{"org/repo@heads/target": "other-sha"},
		createErr: github.NewUnprocessableEntity(),
	}
	if err := newGitHubRefClient(upstream, nil).createRef(context.Background(), "org", "repo", "target", "desired-sha"); err != nil {
		t.Fatalf("recover create race: %v", err)
	}
	if len(upstream.updates) != 1 || upstream.updates[0] != "org/repo@heads/target=desired-sha" {
		t.Fatalf("unexpected updates: %v", upstream.updates)
	}
}

func TestCreateRefValidationFailureIsPermanent(t *testing.T) {
	upstream := &fakeProwRefClient{refs: map[string]string{}, createErr: github.NewUnprocessableEntity()}
	err := newGitHubRefClient(upstream, nil).createRef(context.Background(), "org", "repo", "target", "sha")
	var permanent permanentError
	if !errors.As(err, &permanent) {
		t.Fatalf("expected permanent error, got %T: %v", err, err)
	}
}
